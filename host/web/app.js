"use strict";

const $ = (id) => document.getElementById(id);
const key = location.hash.slice(1) || sessionStorage.getItem("yourlm") || "";
if (location.hash) {
  sessionStorage.setItem("yourlm", key);
  history.replaceState(null, "", location.pathname);
}

let state;
let messages = [];
let pending = false;
let stream = null;
let scanId = 0;
let offered = "";
let lastSession = "";
let loadSample = null;
let cleanupRevision = -1;
const cleanupSelection = new Set();

const gib = (n) =>
  (Number(n || 0) / 1073741824).toLocaleString("pl-PL", {
    maximumFractionDigits: 1,
  });

function humanBytes(value) {
  const bytes = Number(value || 0);
  if (bytes >= 1073741824) return `${(bytes / 1073741824).toLocaleString("pl-PL", { maximumFractionDigits: 2 })} GiB`;
  if (bytes >= 1048576) return `${(bytes / 1048576).toLocaleString("pl-PL", { maximumFractionDigits: 1 })} MiB`;
  return `${Math.max(0, Math.round(bytes / 1024)).toLocaleString("pl-PL")} KiB`;
}

function linkRate(value) {
  return `${humanBytes(value)}/s`;
}

function workerTransport(worker) {
  const selected = worker.transport === "usb" ? "USB/ADB" : "Wi-Fi/LAN";
  const tests = [];
  if (worker.usbBenchmark) {
    tests.push(`USB wysyłanie ${linkRate(worker.usbBenchmark.uploadBps)} · odbiór ${linkRate(worker.usbBenchmark.downloadBps)} · ping ${(Number(worker.usbBenchmark.latencyMicros || 0) / 1000).toLocaleString("pl-PL", { maximumFractionDigits: 1 })} ms`);
  }
  if (worker.wifiBenchmark) {
    tests.push(`Wi-Fi wysyłanie ${linkRate(worker.wifiBenchmark.uploadBps)} · odbiór ${linkRate(worker.wifiBenchmark.downloadBps)} · ping ${(Number(worker.wifiBenchmark.latencyMicros || 0) / 1000).toLocaleString("pl-PL", { maximumFractionDigits: 1 })} ms`);
  }
  return `TRANSPORT: ${selected}${tests.length ? ` · ${tests.join(" · ")}` : ""}`;
}

function elapsed(seconds) {
  const value = Number(seconds || 0);
  const total = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (total < 60) return `${total} s`;
  const minutes = Math.floor(total / 60);
  const rest = total % 60;
  return `${minutes} min ${String(rest).padStart(2, "0")} s`;
}

function loadingTelemetry(session, engine) {
  const now = Date.now();
  const current = {
    key: session?.key || "",
    at: now,
    sent: Number(engine.rpcSent || 0),
    received: Number(engine.rpcReceived || 0),
    read: Number(engine.readBytes || 0),
    cpu: Number(engine.cpuTimeMs || 0),
    log: Number(engine.logBytes || 0),
    stage: Number(engine.progress?.step || 0),
  };
  if (!session || loadSample?.key !== current.key) {
    loadSample = session
      ? { ...current, observedAt: now, activeAt: 0, hasActivity: false, rpcSendRate: 0, rpcReceiveRate: 0, diskRate: 0, cpuRate: 0 }
      : null;
    return loadSample;
  }
  const seconds = (now - loadSample.at) / 1000;
  if (seconds >= 0.5) {
    const sent = Math.max(0, current.sent - loadSample.sent);
    const received = Math.max(0, current.received - loadSample.received);
    const read = Math.max(0, current.read - loadSample.read);
    const cpu = Math.max(0, current.cpu - loadSample.cpu);
    const active =
      sent > 0 ||
      received > 0 ||
      read > 0 ||
      cpu / seconds > 20 ||
      current.log !== loadSample.log ||
      current.stage !== loadSample.stage;
    loadSample = {
      ...current,
      observedAt: loadSample.observedAt,
      activeAt: active ? now : loadSample.activeAt,
      hasActivity: active || loadSample.hasActivity,
      rpcSendRate: sent / seconds,
      rpcReceiveRate: received / seconds,
      diskRate: read / seconds,
      cpuRate: cpu / seconds,
    };
  }
  return loadSample;
}

function loadingActivity(engine, sample, workers) {
  const mib = (value) => (value / 1048576).toLocaleString("pl-PL", { maximumFractionDigits: 1 });
  if (engine.ready) return `Gotowy po ${elapsed(engine.elapsed)}.`;
  if (sample?.rpcSendRate > 1048576) {
    return `Transfer do workera: ${mib(sample.rpcSendRate)} MiB/s · wysłano ${gib(sample.sent)} GiB.`;
  }
  if (sample?.rpcReceiveRate > 1048576) {
    return `Odbiór z workera: ${mib(sample.rpcReceiveRate)} MiB/s · odebrano ${gib(sample.received)} GiB.`;
  }
  if (sample?.diskRate > 1048576) {
    return `Odczyt dysku: ${mib(sample.diskRate)} MiB/s · odczytano ${gib(sample.read)} GiB.`;
  }
  if (sample?.cpuRate > 20) {
    return "CPU pracuje nad analizą modelu lub planem rozmieszczenia wag.";
  }
  const logAge = Number(engine.lastLogAge);
  if (!Number.isFinite(logAge) || logAge < 0) return "Oczekiwanie na pierwszą informację o aktywności silnika…";
  if (logAge < 10) return "Silnik właśnie zgłosił aktywność w logu.";
  return workers.length
    ? `Brak nowej linii logu od ${elapsed(logAge)}; sprawdzamy dalej CPU, dysk i transfer do workera.`
    : `Brak nowej linii logu od ${elapsed(logAge)}; sprawdzamy dalej CPU i odczyt dysku.`;
}

function loadingStalled(engine, sample) {
  if (engine.ready || !sample || Number(engine.elapsed || 0) < 180) return false;
  const now = Date.now();
  const observed = Math.max(0, (now - sample.observedAt) / 1000);
  const quiet = sample.hasActivity ? Math.max(0, (now - sample.activeAt) / 1000) : observed;
  const recentLog = Number(engine.lastLogAge) >= 0 && Number(engine.lastLogAge) < 15;
  return !recentLog && observed >= 60 && quiet >= 60;
}

function loadingDecision(engine, sample) {
  if (engine.ready) return "Ładowanie zakończone — można przejść do czatu.";
  const quiet = sample?.hasActivity ? Math.max(0, (Date.now() - sample.activeAt) / 1000) : Infinity;
  const recentLog = Number(engine.lastLogAge) >= 0 && Number(engine.lastLogAge) < 15;
  if (engine.elapsed < 45) return "To wczesny etap — na razie warto czekać.";
  if (recentLog || (sample?.hasActivity && quiet < 15)) {
    return "Widać mierzalną aktywność — warto nadal czekać.";
  }
  if (loadingStalled(engine, sample)) {
    return "Od co najmniej minuty nie widać aktywności. Możesz zatrzymać i spróbować bez workera.";
  }
  return "Brak dokładnej ETA; pasek pokazuje potwierdzony etap, a nie udawany procent pliku.";
}

function note(value) {
  $("notice").textContent = value;
  $("notice").hidden = !value;
}

async function api(op, params) {
  const response = await fetch("/api/" + op, {
    method: params ? "POST" : "GET",
    headers: { "X-YourLM": key, "Content-Type": "application/json" },
    body: params ? JSON.stringify(params) : undefined,
  });
  if (!response.ok) throw Error((await response.text()).trim());
  return response.json();
}

function el(name, text) {
  const item = document.createElement(name);
  item.textContent = text;
  return item;
}

function options(id, values, first) {
  const select = $(id);
  const signature = JSON.stringify(values);
  if (select.dataset.vals === signature) return;
  const previous = select.value;
  select.replaceChildren();
  if (first) select.add(new Option(first, ""));
  for (const value of values) {
    const title = id === "library" ? value.split(/[\\/]/).pop() : value;
    select.add(new Option(title, value));
  }
  select.dataset.vals = signature;
  if (values.includes(previous)) select.value = previous;
}

function device(name, role, meta, address) {
  const row = el("div", "");
  row.className = "node";
  const icon = el("span", address ? "TEL" : "PC");
  icon.className = "icon";
  const details = el("div", "");
  details.append(el("b", name), el("small", meta));
  row.append(icon, details, el("em", role));
  if (address) {
    const remove = el("button", "Odłącz");
    remove.disabled = Boolean(state.session);
    remove.onclick = () => act("remove", { address });
    row.append(remove);
  }
  return row;
}

function usbRow(item) {
  const row = el("div", "");
  row.className = "usbDeviceRow";
  const icon = el("span", "USB");
  icon.className = "icon";
  const details = el("div", "");
  const title = item.model || item.name || item.serial;
  const status = item.state === "device"
    ? "Gotowy"
    : item.state === "unauthorized"
      ? "Czeka na zgodę na telefonie"
      : item.state;
  details.append(el("b", title), el("small", `${item.serial} · ${status}`));
  const badge = el("em", item.state === "device" ? "GOTOWY" : "UWAGA");
  row.append(icon, details, badge);
  return row;
}

function hfBadge(value, warning = false) {
  const badge = el("span", value);
  badge.className = `hfBadge${warning ? " warn" : ""}`;
  return badge;
}

function hfModelRow(model) {
  const row = el("div", "");
  row.className = "hfResult";
  const details = el("div", "");
  details.append(
    el("b", model.id),
    el("small", `${Number(model.downloads || 0).toLocaleString("pl-PL")} pobrań · ${Number(model.likes || 0).toLocaleString("pl-PL")} polubień`),
  );
  if (model.gated) details.append(hfBadge("WYMAGA ZGODY / KONTA", true));
  else details.append(hfBadge("PUBLICZNY GGUF"));
  const choose = el("button", model.gated ? "Model ograniczony" : "Pokaż warianty");
  choose.className = model.gated ? "secondary" : "";
  choose.disabled = Boolean(model.gated);
  choose.onclick = () => act("hf-files", { repo: model.id }, $("hfDlg"), false);
  row.append(details, choose);
  return row;
}

function hfFileRow(file) {
  const row = el("div", "");
  row.className = `hfFile${file.recommended ? " recommended" : ""}`;
  const details = el("div", "");
  details.append(el("b", file.name));
  const meta = [humanBytes(file.size), file.quantization || "kwantyzacja z nazwy nierozpoznana"];
  details.append(el("small", meta.join(" · ")));
  if (file.recommended) details.append(hfBadge("POLECANY START"));
  if (file.quantization) details.append(hfBadge(file.quantization));
  if (file.recommendation) details.append(el("small", file.recommendation));
  const download = el("button", "Pobierz i wybierz");
  download.onclick = async () => {
    if (Number(file.size || 0) > 12 * 1073741824 && !confirm(`Ten plik ma ${humanBytes(file.size)}. Pobrać go do prywatnej biblioteki YourLM?`)) return;
    if (await act("hf-download", { repo: file.repo, file: file.path }, $("hfDlg"), false)) $("hfDlg").close();
  };
  row.append(details, download);
  return row;
}

function renderHuggingFace(hf = {}) {
  const download = hf.download || {};
  const showDownload = Boolean(download.active || download.complete || download.canceled || download.error);
  $("hfDownload").hidden = !showDownload;
  if (showDownload) {
    const total = Number(download.total || 0);
    const received = Number(download.received || 0);
    const percent = total > 0 ? Math.min(100, Math.max(0, received / total * 100)) : 0;
    $("hfDownloadName").textContent = (download.file || "model GGUF").split("/").pop();
    $("hfDownloadPercent").textContent = download.verifying
      ? "weryfikacja"
      : total > 0
        ? `${percent.toFixed(1)}%`
        : "przygotowanie";
    $("hfProgressFill").style.width = `${percent}%`;
    $("hfDownload").querySelector(".hfProgress").setAttribute("aria-valuenow", String(percent));
    const speed = Number(download.bytesPerSecond || 0);
    const pieces = [download.message || ""];
    if (total > 0) pieces.push(`${humanBytes(received)} / ${humanBytes(total)}`);
    if (speed > 0) pieces.push(`${humanBytes(speed)}/s`);
    if (Number(download.etaSeconds || 0) > 0) pieces.push(`pozostało ok. ${elapsed(download.etaSeconds)}`);
    if (download.error) pieces.push(download.error);
    $("hfDownloadMeta").textContent = pieces.filter(Boolean).join(" · ");
    $("hfCancel").hidden = !download.active;
    $("hfCancel").disabled = !download.active;
    $("hfRetry").hidden = download.active || download.complete || !(download.canceled || download.error);
    $("hfRetry").disabled = download.active;
    $("hfRetry").dataset.repo = download.repo || "";
    $("hfRetry").dataset.file = download.file || "";
  }

  const results = hf.results || [];
  const files = hf.files || [];
  $("hfStatus").textContent = hf.searching
    ? "Wyszukiwanie publicznych repozytoriów GGUF…"
    : hf.repo
      ? `${hf.repo} · ${files.length} jednoplikowych wariantów GGUF`
      : results.length
        ? `${results.length} wyników, najpopularniejsze najpierw`
        : hf.query
          ? "Brak wyników. Spróbuj krótszej nazwy modelu."
          : "Wpisz nazwę albo wyszukaj najpopularniejsze GGUF.";
  $("hfDlg").querySelector(".error").textContent = hf.error || "";
  if (document.activeElement !== $("hfQuery") && hf.query !== undefined) $("hfQuery").value = hf.query || "";
  if (hf.repo) {
    const back = el("button", "← Wróć do wyników");
    back.className = "secondary hfBack";
    back.onclick = () => act("hf-search", { query: hf.query || "" }, $("hfDlg"), false);
    $("hfResults").replaceChildren(back);
    $("hfFiles").replaceChildren(...files.map(hfFileRow));
  } else {
    $("hfResults").replaceChildren(...results.map(hfModelRow));
    $("hfFiles").replaceChildren();
  }
}

function cleanupSelectedApps(cleanup = state?.cleanup || {}) {
  const selected = new Set(cleanupSelection);
  return (cleanup.apps || []).filter((app) => app.closable && selected.has(Number(app.pid)));
}

function updateCleanupSelection(cleanup = state?.cleanup || {}, session = state?.session) {
  const selected = cleanupSelectedApps(cleanup);
  const total = selected.reduce((sum, app) => sum + Number(app.resident || 0), 0);
  $("cleanupSelection").textContent = selected.length
    ? `${selected.length} ${selected.length === 1 ? "aplikacja" : "aplikacje"} · około ${humanBytes(total)} RAM roboczego`
    : "Nic nie zaznaczono.";
  $("cleanupClose").disabled = !selected.length || Boolean(session) || Boolean(cleanup.busy);
}

function cleanupAppRow(app) {
  const row = el("label", "");
  row.className = `cleanupApp${app.closable ? "" : " protected"}`;
  const checkWrap = el("span", "");
  checkWrap.className = "cleanupCheck";
  const check = document.createElement("input");
  check.type = "checkbox";
  check.disabled = !app.closable;
  check.checked = cleanupSelection.has(Number(app.pid));
  check.setAttribute("aria-label", app.closable ? `Zaznacz ${app.title}` : `${app.title} jest chroniona`);
  check.onchange = () => {
    if (check.checked) cleanupSelection.add(Number(app.pid));
    else cleanupSelection.delete(Number(app.pid));
    updateCleanupSelection();
  };
  checkWrap.append(check);
  const details = el("div", "");
  details.append(el("b", app.title || app.name));
  const processMeta = `${app.name} · PID ${app.pid} · ${Number(app.processes || 1)} ${Number(app.processes || 1) === 1 ? "proces" : "procesy"}`;
  details.append(el("small", app.closable ? processMeta : `${processMeta} · ${app.protectedReason || "chroniona"}`));
  const memory = el("span", `~${humanBytes(app.resident)}`);
  memory.className = "cleanupMemory";
  row.append(checkWrap, details, memory);
  return row;
}

function cleanupOutcomeRow(outcome) {
  const row = el("div", "");
  row.className = `cleanupOutcome ${outcome.status || "not-requested"}`;
  const details = el("div", "");
  details.append(
    el("b", outcome.title || outcome.name),
    el("small", `${outcome.name} · PID ${outcome.pid} · ${outcome.detail || "Brak szczegółów."}`),
  );
  const labels = {
    closed: "ZAMKNIĘTA",
    "closed-unconfirmed": "ZAMKNIĘTA?",
    remaining: "NADAL OTWARTA",
    "not-requested": "NIEWYSŁANA",
  };
  const badge = el("span", labels[outcome.status] || "SPRAWDŹ");
  row.append(details, badge);
  return row;
}

function renderCleanup(cleanup = {}, session) {
  $("cleanupCard").hidden = !cleanup.supported;
  if (!cleanup.supported) return;
  $("cleanupScan").disabled = Boolean(cleanup.busy);
  $("cleanupScan").textContent = cleanup.busy ? "Sprawdzam…" : "Sprawdź aplikacje";
  const revision = Number(cleanup.revision || 0);
  if (revision !== cleanupRevision) {
    cleanupRevision = revision;
    cleanupSelection.clear();
    const apps = cleanup.apps || [];
    if (!cleanup.error && !(cleanup.outcomes || []).length) {
      for (const app of apps.filter((item) => item.closable).slice(0, 32)) {
        cleanupSelection.add(Number(app.pid));
      }
    }
    $("cleanupApps").replaceChildren(...apps.map(cleanupAppRow));
    if (revision > 0 && !apps.length) {
      const empty = el("div", "Nie znaleziono zwykłych aplikacji z widocznymi oknami.");
      empty.className = "infoStrip";
      $("cleanupApps").append(empty);
    }
  }
  const apps = cleanup.apps || [];
  $("cleanupActions").hidden = !apps.some((app) => app.closable);
  let result = cleanup.error || cleanup.message || "Kliknij „Sprawdź aplikacje”, aby zobaczyć przybliżony RAM widocznych programów.";
  if ((cleanup.outcomes || []).length) {
    const before = Number(cleanup.beforeAvailable || 0);
    const after = Number(cleanup.afterAvailable || 0);
    result += ` Dostępny RAM: ${humanBytes(before)} → ${humanBytes(after)}.`;
    if (after > before) result += ` Wzrost o około ${humanBytes(after - before)}.`;
    else if (after < before) result += ` Chwilowy pomiar jest niższy o ${humanBytes(before - after)}; użycie systemu może zmieniać się w tle.`;
    else result += " Pomiar nie wykazał zmiany.";
  }
  $("cleanupResult").textContent = result;
  $("cleanupResult").classList.toggle("bad", Boolean(cleanup.error));
  $("cleanupOutcomes").replaceChildren(...(cleanup.outcomes || []).map(cleanupOutcomeRow));
  updateCleanupSelection(cleanup, session);
}

function render(nextState) {
  state = nextState;
  $("build").textContent = `v${state.version} · b12562`;
  $("build").title = `Commit źródłowy: ${state.commit}`;

  const update = state.update || {};
  $("updateBar").hidden = !(update.available || update.downloading || update.ready);
  $("updateTitle").textContent = update.ready
    ? `YourLM ${update.latest} jest gotowy`
    : update.downloading
      ? `Pobieranie YourLM ${update.latest}…`
      : `Dostępny YourLM ${update.latest}`;
  $("updateText").textContent = update.progress || "Nowa wersja zachowa modele i ustawienia.";
  $("updateAction").textContent = update.ready
    ? "Uruchom ponownie"
    : update.downloading
      ? "Pobieranie…"
      : "Pobierz aktualizację";
  $("updateAction").disabled = Boolean(update.downloading || update.checking);
  $("updateCenterText").textContent = update.error || update.progress ||
    `Aktualna wersja: ${update.current || state.version}. Aktualizacje są sprawdzane automatycznie.`;
  $("releaseLink").hidden = !update.pageUrl;
  if (update.pageUrl) $("releaseLink").href = update.pageUrl;
  const shortcutText = update.ready
    ? "Uruchom ponownie"
    : update.downloading
      ? "Pobieranie…"
      : update.available
        ? `Pobierz ${update.latest}`
        : update.checking
          ? "Sprawdzanie…"
          : update.error
            ? "Spróbuj ponownie"
            : `Sprawdź aktualizacje · ${update.current || state.version}`;
  for (const button of document.querySelectorAll(".updateShortcut")) {
    button.disabled = Boolean(update.downloading || update.checking);
    button.classList.toggle("available", Boolean(update.available || update.ready));
    const text = button.querySelector(".updateShortcutText");
    if (text) text.textContent = shortcutText;
  }

  const usb = state.usb || { devices: [] };
  const usbDevices = usb.devices || [];
  options(
    "usbDevice",
    usbDevices.filter((item) => item.state === "device").map((item) => item.serial),
  );
  for (const option of $("usbDevice").options) {
    const found = usbDevices.find((item) => item.serial === option.value);
    if (found) option.textContent = found.model || found.name || found.serial;
  }
  $("usbSelectLabel").hidden = usbDevices.filter((item) => item.state === "device").length < 2;
  $("usbDevices").replaceChildren(
    ...(usbDevices.length
      ? usbDevices.map(usbRow)
      : [el("p", usb.message || "Nie wykryto telefonu.")]),
  );
  $("usbTitle").textContent = usb.installing
    ? "Instalowanie YourLM…"
    : usb.ready
      ? "Telefon jest gotowy"
      : usbDevices.some((item) => item.state === "unauthorized")
        ? "Potwierdź debugowanie na telefonie"
        : "Podłącz telefon przez USB";
  $("usbText").textContent = usb.message || "Włącz debugowanie USB i zaakceptuj połączenie.";
  $("usbInstall").textContent = usb.installing ? "Instalowanie…" : "Zainstaluj lub zaktualizuj YourLM";
  $("usbInstall").disabled = !usb.ready || !usb.apkReady || usb.installing;
  $("usbRefresh").disabled = Boolean(usb.installing);
  $("usbRefresh").textContent = (state.nodes || []).some((node) => node.platform === "android")
    ? "Odśwież USB i powtórz test"
    : "Odśwież urządzenia";

  const workers = state.nodes || [];
  const session = state.session;
  const engineState = state.engine || {};
  const memory = state.memory || {};
  const loadTelemetry = loadingTelemetry(session, engineState);
  const totalRam = Number(memory.physical ?? workers.reduce((sum, node) => sum + node.total, state.total));
  const availableRam = Number(memory.available ?? workers.reduce((sum, node) => sum + node.free, state.free));
  const osUsed = Number(memory.osUsed ?? Math.max(0, totalRam - availableRam));
  const mappedModel = Number(memory.mappedModel || 0);
  const actualAllocation = Number(memory.actualAllocation || 0);
  $("count").textContent = 1 + workers.length;
  $("ram").textContent = gib(totalRam) + " GiB";
  $("usedRam").textContent = gib(osUsed) + " GiB";
  $("capacity").textContent = gib(availableRam) + " GiB";
  $("mappedRam").textContent = mappedModel ? gib(mappedModel) + " GiB" : "—";
  $("processRam").textContent = actualAllocation ? gib(actualAllocation) + " GiB" : session ? "pomiar…" : "0 GiB";
  $("capacity").title = "Pamięć natychmiast dostępna według systemów. Nie jest limitem rozmiaru mapowanego modelu.";
  const commitAvailable = Number(memory.commitAvailable || 0);
  const pageFile = Number(memory.pageFile || 0);
  $("commitTitle").textContent = state.os === "windows"
    ? "Windows: pamięć mapowana i plik stronicowania"
    : "Host: pamięć mapowana i limit systemu";
  $("commitRam").textContent = state.os === "windows"
    ? `${gib(commitAvailable)} GiB wolnego limitu zatwierdzeń · ${gib(pageFile)} GiB pliku stronicowania`
    : `${gib(commitAvailable)} GiB wolnego limitu zatwierdzeń hosta`;
  $("commitHelp").textContent = mappedModel
    ? `Wybrany plik ${gib(mappedModel)} GiB jest mapowany jako strony pliku; zajętość RAM rośnie według rzeczywiście używanych stron i buforów.`
    : "Wybierz GGUF, aby zobaczyć jego pełny rozmiar mapowania osobno od wolnego RAM.";
  renderCleanup(state.cleanup || {}, session);

  if (state.offer) {
    $("mode").textContent = "Ten komputer jest workerem";
    $("topdesc").textContent = "Oczekuje na bezpieczne połączenie z innym hostem YourLM.";
  } else if (workers.length) {
    $("mode").textContent = workers.length === 1 ? "Laptop + telefon" : `Host + ${workers.length} workery`;
    $("topdesc").textContent = workers.some((worker) => worker.platform === "android")
      ? "YourLM porównuje szyfrowane Wi-Fi z tunelem USB/ADB i wybiera szybszą dostępną drogę."
      : "Urządzenia są połączone szyfrowanym tunelem w prywatnej sieci.";
  } else {
    $("mode").textContent = "Tylko ten komputer";
    $("topdesc").textContent = "Możesz od razu wybrać model albo dodać telefon jako worker.";
  }

  $("nodes").replaceChildren(
    device(
      state.host,
      "HOST",
      `${state.os === "windows" ? "Windows" : "Android"} · ${state.cores} wątków · ${gib(state.total)} GiB fizyczne · ${gib(memory.host?.used || Math.max(0, state.total - state.free))} GiB system i aplikacje · ${gib(state.free)} GiB dostępne teraz · ${gib(memory.hostProcess?.resident || 0)} GiB silnik YourLM · mmap bez limitu planisty`,
    ),
    ...workers.map((worker) =>
      device(
        worker.name,
        worker.recovering ? "ODTWARZANIE" : worker.recoveryError ? "BŁĄD" : "WORKER",
        `${worker.recovering ? `ODTWARZANIE SILNIKA (próba ${worker.restartAttempts || 1}) · ` : worker.recoveryError ? "ODZYSKANIE NIEUDANE · " : ""}${workerTransport(worker)} · ${worker.transportReason || "Szyfrowany kanał z przypiętym certyfikatem."} · ${worker.address} · ${worker.cores} wątków · ${gib(worker.total)} GiB fizyczne · ${gib(worker.used || Math.max(0, worker.total - worker.free))} GiB system i aplikacje · ${gib(worker.free)} GiB dostępne teraz · ${gib(worker.allocation || 0)} GiB silnik · pamięć RPC włączona`,
        worker.address,
      ),
    ),
  );

  $("next").hidden = Boolean(state.offer);
  $("next").textContent = session?.ready
    ? "Otwórz czat"
    : state.model.name
      ? "Uruchom model bez limitu"
      : "Wybierz model";
  for (const id of ["add", "addTop"]) $(id).disabled = Boolean(session || state.offer);

  $("offer").hidden = state.os !== "windows";
  options("workerIP", state.ips.slice(1));
  $("offerBtn").textContent = state.offer ? "Zatrzymaj worker" : "Uruchom worker";
  $("offerCode").value = state.offer || "";
  $("offerCode").hidden = true;
  $("offerShare").hidden = !state.offer;
  $("offerAddress").value = state.offerAddress || "";
  $("offerKey").value = state.offerKey || "";
  if (state.offer !== offered) {
    offered = state.offer;
    $("winQR").replaceChildren();
    if (offered) {
      const qr = qrcode(0, "M");
      qr.addData(offered);
      qr.make();
      $("winQR").innerHTML = qr.createSvgTag(4, 16);
    }
  }

  $("engine").textContent = state.runtime
    ? "Wbudowany silnik jest gotowy. Nie potrzebujesz dodatkowego folderu runtime."
    : "Nie udało się przygotować wbudowanego silnika. Uruchom ponownie YourLM.exe lub pobierz aktualną wersję.";
  document.querySelector(".runtimeStatus").classList.toggle("bad", !state.runtime);
  $("selected").textContent = state.model.name
    ? `${state.model.name} · ${gib(state.model.size)} GiB · ${state.model.layers} warstw`
    : "Nie wybrano pliku.";
  $("advice").textContent = state.advice || "Po wybraniu pliku pokażemy ilość dostępnej pamięci. Ostateczny podział dobierze llama.cpp przy starcie.";
  $("advice").classList.toggle("warn", Boolean(state.modelWarning));
  $("pick").hidden = state.os !== "windows";
  $("pick").disabled = $("manual").disabled = $("hfBrowse").disabled = Boolean(session);
  renderHuggingFace(state.huggingFace || {});
  options("library", state.models || [], "Wybierz z prywatnej biblioteki…");
  $("library").hidden = !state.models.length;
  $("library").disabled = Boolean(session);
  $("libdir").textContent = "Biblioteka: " + state.library;

  options("bind", state.ips);
  $("bind").disabled = $("port").disabled = Boolean(session);
  $("run").textContent = session ? "Zatrzymaj model" : "Uruchom model bez limitu";
  $("run").disabled = !session && (!state.model.name || !state.runtime || Boolean(state.offer));
  const hasEngineDetails = Boolean(session || engineState.log || engineState.path);
  $("sessionInfo").hidden = !hasEngineDetails;
  $("sessionInfo").classList.toggle("loading", Boolean(session && !session.ready));
  $("sessionInfo").classList.toggle("failed", Boolean(!session && state.last));
  $("apiDetails").hidden = !session;
  $("engineDiagnostics").hidden = !hasEngineDetails;
  $("loadProgressPanel").hidden = !session;
  if (session) {
    const progress = engineState.progress || {
      step: session.ready ? 6 : 1,
      steps: 6,
      label: engineState.phase || "Uruchamianie silnika",
      ready: Boolean(session.ready),
    };
    const steps = Math.max(1, Number(progress.steps || 6));
    const step = Math.min(steps, Math.max(1, Number(progress.step || 1)));
    const progressReady = Boolean(progress.ready || session.ready);
    const completed = progressReady ? steps : step - 1;
    $("loadStepLabel").textContent = progress.label;
    $("loadStepCount").textContent = progressReady
      ? `${steps} z ${steps} · GOTOWY`
      : `Etap ${step} z ${steps} · orientacyjnie`;
    $("loadProgressFill").style.width = `${(completed / steps) * 100}%`;
    $("loadProgressTrack").classList.toggle("loading", !session.ready);
    $("loadProgressTrack").classList.toggle("ready", Boolean(session.ready));
    $("loadProgressTrack").setAttribute("aria-valuemax", String(steps));
    $("loadProgressTrack").setAttribute("aria-valuenow", String(step));
    $("loadProgressTrack").setAttribute(
      "aria-valuetext",
      session.ready ? "Model gotowy" : `Etap ${step} z ${steps}: ${progress.label}`,
    );
    $("loadActivity").textContent = loadingActivity(engineState, loadTelemetry, workers);
    $("loadDecisionText").textContent = loadingDecision(engineState, loadTelemetry);
    $("loadProgressPanel").classList.toggle("stalled", loadingStalled(engineState, loadTelemetry));
    $("cancelLoad").hidden = Boolean(session.ready);
  }
  if (hasEngineDetails) {
    $("runstate").textContent = engineState.phase || (session?.ready ? "Model gotowy" : "Ostatnia sesja silnika");
    $("loadMeta").textContent = session
      ? `${engineState.mode || "Tryb lokalny"} · czas ${elapsed(engineState.elapsed)}`
      : "Ostatnia diagnostyka jest dostępna poniżej.";
    const loading = Boolean(session && !session.ready);
    const advisory = /control-looking token|tensor overrides to cpu.*mmap/i.test(
      engineState.log || "",
    );
    $("loadHint").hidden = !loading;
    $("loadHint").textContent = loading
      ? (workers.length
          ? "Pierwszy start rozdziela i przesyła wagi wybranym łączem. Przed startem YourLM porównuje Wi-Fi i dostępny tunel USB/ADB; pokazywana szybkość RPC obejmuje też TLS, zapis cache i pracę telefonu. Kolejne uruchomienia mogą użyć pamięci podręcznej RPC zapisywanej porcjami do 16 MiB. Windows zapisuje migawkę Redmi co 3 sekundy i dołączy ostatnią udaną nawet po utracie listenera telefonu."
          : "Windows odczytuje i mapuje pełny plik GGUF. Czas zależy od jego pełnego rozmiaru, szybkości dysku oraz dostępnego RAM-u i pliku stronicowania.") +
        (advisory ? " Widoczne ostrzeżenia o tokenach lub mmap są informacyjne — silnik nadal ładuje model." : "")
      : "";
    $("engineLogPath").textContent = engineState.path ? `Plik: ${engineState.path}` : "";
    $("engineLog").textContent = engineState.log || "Oczekiwanie na pierwszy komunikat llama.cpp…";
  }
  if (session) $("endpoint").textContent = session.url;

  $("chatstatus").textContent = session?.ready
    ? `${session.name} działa lokalnie.`
    : session
      ? "Model jest ładowany. To może potrwać kilka minut."
      : "Najpierw wybierz i uruchom model w zakładce Model i API.";
  $("send").querySelector("button").disabled = !session?.ready || pending;
  $("prompt").disabled = !session?.ready || pending;
  if (lastSession && lastSession !== session?.key) {
    messages = [];
    $("speed").textContent = "";
    drawChat();
  }
  lastSession = session?.key || "";
  if (state.last && state.last !== render.last) note(state.last);
  render.last = state.last;
}

async function act(op, data = {}, dialog, closeDialog = true) {
  try {
    note("");
    if (dialog) dialog.querySelector(".error").textContent = "";
    const updated = await api(op, data);
    if (updated.restarting) {
      note("Aktualizacja jest instalowana. YourLM uruchomi się ponownie za chwilę.");
      return true;
    }
    render(updated);
    if (dialog && closeDialog) dialog.close();
    return true;
  } catch (error) {
    if (dialog) dialog.querySelector(".error").textContent = error.message;
    else note(error.message);
    return false;
  }
}

function openTab(name) {
  for (const tab of document.querySelectorAll(".tab")) tab.hidden = tab.id !== name;
  for (const button of document.querySelectorAll("nav button")) button.classList.toggle("sel", button.dataset.tab === name);
  document.title = `YourLM · ${{ cluster: "Urządzenia", model: "Model i API", chat: "Czat", phone: "Telefon USB" }[name]}`;
  window.scrollTo({ top: 0, behavior: "smooth" });
}

document.querySelectorAll("nav button").forEach((button) => {
  button.onclick = () => openTab(button.dataset.tab);
});

$("next").onclick = () => openTab(state?.session?.ready ? "chat" : "model");
$("usbRefresh").onclick = async () => {
  $("usbRefresh").disabled = true;
  $("usbRefresh").textContent = "Mierzę USB i Wi-Fi…";
  await act("usb-refresh");
  if (state) render(state);
};
$("usbInstall").onclick = () => act("usb-install", { serial: $("usbDevice").value });
$("checkUpdate").onclick = () => act("update-check");
function updateOperation() {
  if (state?.update?.ready) return "update-apply";
  if (state?.update?.available) return "update-download";
  return "update-check";
}
$("updateAction").onclick = () => act(updateOperation());
document.querySelectorAll(".updateShortcut").forEach((button) => {
  button.onclick = () => act(updateOperation());
});
function openPairing() {
  $("pair").showModal();
  $("pair").querySelector(".error").textContent = "";
  setTimeout(() => $("pairAddress").focus(), 50);
}
$("add").onclick = openPairing;
$("addTop").onclick = openPairing;
$("manual").onclick = () => $("pathDlg").showModal();
$("hfBrowse").onclick = () => {
  $("hfDlg").showModal();
  $("hfDlg").querySelector(".error").textContent = "";
  setTimeout(() => $("hfQuery").focus(), 50);
};
$("hfSearch").onclick = async () => {
  $("hfSearch").disabled = true;
  $("hfSearch").textContent = "Szukam…";
  await act("hf-search", { query: $("hfQuery").value.trim() }, $("hfDlg"), false);
  $("hfSearch").disabled = false;
  $("hfSearch").textContent = "Szukaj";
};
$("hfQuery").onkeydown = (event) => {
  if (event.key === "Enter") {
    event.preventDefault();
    $("hfSearch").click();
  }
};
$("hfCancel").onclick = () => act("hf-cancel");
$("hfRetry").onclick = () =>
  act("hf-download", {
    repo: $("hfRetry").dataset.repo,
    file: $("hfRetry").dataset.file,
  });
document.querySelectorAll(".close").forEach((button) => {
  button.onclick = () => button.closest("dialog").close();
});

$("pairManualBtn").onclick = async () => {
  const button = $("pairManualBtn");
  button.disabled = true;
  button.textContent = "Paruję i mierzę łącza…";
  await act(
    "pair-manual",
    { pairAddress: $("pairAddress").value.trim(), pairKey: $("pairKey").value.trim() },
    $("pair"),
  );
  button.disabled = false;
  button.textContent = "Połącz telefon";
};
$("pairBtn").onclick = async () => {
  const button = $("pairBtn");
  button.disabled = true;
  button.textContent = "Paruję i mierzę łącza…";
  await act("pair", { code: $("code").value }, $("pair"));
  button.disabled = false;
  button.textContent = "Połącz pełnym kodem";
};
$("pairKey").oninput = (event) => {
  event.target.value = event.target.value.toUpperCase().replace(/[^A-Z0-9-]/g, "");
};
$("choose").onclick = () => act("model", { path: $("path").value }, $("pathDlg"));
$("library").onchange = (event) => event.target.value && act("model", { path: event.target.value });
$("pick").onclick = () => act("pick");
$("offerBtn").onclick = () => act(state.offer ? "unoffer" : "offer", { bind: $("workerIP").value });
$("cancelLoad").onclick = () => act("stop");
$("run").onclick = async () => {
  if (
    !state.session &&
    $("bind").value !== "127.0.0.1" &&
    !confirm("Udostępnić API przez zwykły HTTP w tej zaufanej sieci prywatnej?")
  ) return;
  const starting = !state.session;
  if (starting && (state.nodes || []).some((node) => node.platform === "android")) {
    $("run").disabled = true;
    $("run").textContent = "Mierzę USB i Wi-Fi…";
  }
  const ok = await act(state.session ? "stop" : "start", {
    bind: $("bind").value,
    port: Number($("port").value),
  });
  if (!ok) render(state);
};

function stopScan() {
  scanId++;
  stream?.getTracks().forEach((track) => track.stop());
  stream = null;
  $("video").hidden = true;
  $("scan").disabled = false;
  $("scan").textContent = "Zeskanuj QR";
}

$("pair").onclose = () => {
  stopScan();
  $("code").value = "";
};

$("scan").onclick = async () => {
  const id = ++scanId;
  $("scan").disabled = true;
  $("scan").textContent = "Szukam kodu…";
  try {
    const camera = await navigator.mediaDevices.getUserMedia({
      video: { width: 720, facingMode: { ideal: "environment" } },
    });
    if (scanId !== id) {
      camera.getTracks().forEach((track) => track.stop());
      return;
    }
    stream = camera;
    const video = $("video");
    video.srcObject = camera;
    video.hidden = false;
    await video.play();
    const canvas = document.createElement("canvas");
    const context = canvas.getContext("2d", { willReadFrequently: true });
    function scanFrame() {
      if (scanId !== id) return;
      if (video.videoWidth) {
        canvas.width = video.videoWidth;
        canvas.height = video.videoHeight;
        context.drawImage(video, 0, 0);
        const image = context.getImageData(0, 0, canvas.width, canvas.height);
        const qr = jsQR(image.data, image.width, image.height);
        if (qr?.data.startsWith("YLM1|")) {
          $("code").value = qr.data;
          stopScan();
          $("pairBtn").click();
          return;
        }
      }
      setTimeout(scanFrame, 160);
    }
    scanFrame();
  } catch (error) {
    stopScan();
    $("pair").querySelector(".error").textContent =
      "Kamera jest niedostępna. Przepisz adres i kod pokazane na telefonie.";
  }
};

$("cleanupScan").onclick = async () => {
  $("cleanupScan").disabled = true;
  $("cleanupScan").textContent = "Sprawdzam…";
  await act("cleanup-scan");
  renderCleanup(state?.cleanup || {}, state?.session);
};

$("cleanupClose").onclick = async () => {
  const selected = cleanupSelectedApps();
  if (!selected.length) return;
  const total = selected.reduce((sum, app) => sum + Number(app.resident || 0), 0);
  const names = selected.slice(0, 5).map((app) => `• ${app.title || app.name}`).join("\n");
  const more = selected.length > 5 ? `\n• oraz ${selected.length - 5} kolejnych` : "";
  if (!confirm(`Zapisz niezapisaną pracę. YourLM wyśle zwykłą prośbę o zamknięcie — bez wymuszania.\n\n${names}${more}\n\nPrzybliżony RAM: ${humanBytes(total)}. Kontynuować?`)) return;
  $("cleanupClose").disabled = true;
  $("cleanupClose").textContent = "Zamykam łagodnie…";
  await act("cleanup-close", { pids: selected.map((app) => Number(app.pid)) });
  $("cleanupClose").textContent = "Zamknij wybrane łagodnie";
  renderCleanup(state?.cleanup || {}, state?.session);
};

async function copy(value) {
  try {
    await navigator.clipboard.writeText(value);
    note("Skopiowano do schowka.");
  } catch {
    const area = document.createElement("textarea");
    area.value = value;
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.append(area);
    area.select();
    const ok = document.execCommand("copy");
    area.remove();
    note(ok ? "Skopiowano do schowka." : "Schowek jest niedostępny.");
  }
}

$("copyOffer").onclick = () => copy(`Adres: ${state.offerAddress}\nKod: ${state.offerKey}`);
$("copyEngineLog").onclick = () => copy(
  `YourLM ${state.version}\n${state.engine?.phase || "Diagnostyka silnika"}\n${state.engine?.path || ""}\n\n${state.engine?.log || "Brak komunikatów w logu."}`,
);
$("copyKey").onclick = () => copy(state.session.key);
$("copyCfg").onclick = () =>
  copy(
    `name: YourLM\nversion: 1.0.0\nschema: v1\nmodels:\n  - name: YourLM lokalny\n    provider: openai\n    model: yourlm\n    apiBase: ${state.session.url}\n    apiKey: ${state.session.key}\n    roles: [chat, edit, apply]`,
  );

function drawChat() {
  const list = $("messages");
  if (!messages.length) {
    const empty = el("div", "");
    empty.className = "emptyChat";
    empty.append(el("span", "Y"), el("h2", "Twoja prywatna rozmowa"), el("p", "Wiadomości pozostają tylko w pamięci tej karty."));
    list.replaceChildren(empty);
    return;
  }
  list.replaceChildren(
    ...messages.map((message) => {
      const bubble = el("div", "");
      bubble.className = "bubble " + (message.role === "user" ? "user" : "assistant");
      bubble.append(
        el("b", message.role === "user" ? "TY" : "YOUR LM"),
        document.createTextNode(message.content),
      );
      return bubble;
    }),
  );
  list.scrollTop = list.scrollHeight;
}

$("clear").onclick = () => {
  if (!pending) {
    messages = [];
    drawChat();
  }
};

$("prompt").onkeydown = (event) => {
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
    event.preventDefault();
    $("send").requestSubmit();
  }
};

$("send").onsubmit = async (event) => {
  event.preventDefault();
  const prompt = $("prompt").value.trim();
  if (!prompt || pending) return;
  pending = true;
  const session = state.session?.key;
  messages.push({ role: "user", content: prompt });
  $("prompt").value = "";
  drawChat();
  render(state);
  try {
    const response = await api("chat", { messages });
    if (session === state.session?.key) {
      const answer = response.choices?.[0]?.message?.content;
      if (typeof answer !== "string") throw Error("Silnik nie zwrócił tekstu odpowiedzi");
      messages.push({ role: "assistant", content: answer });
      const rate = response.timings?.predicted_per_second;
      if (Number.isFinite(rate)) $("speed").textContent = `Ostatnia odpowiedź: ${rate.toFixed(1)} tokenów/s`;
    }
  } catch (error) {
    note(error.message);
    if (session === state.session?.key) {
      messages.pop();
      $("prompt").value = prompt;
    }
  } finally {
    pending = false;
    drawChat();
    render(state);
  }
};

async function refresh() {
  try {
    render(await api("state"));
  } catch (error) {
    note(error.message);
  } finally {
    setTimeout(refresh, state?.huggingFace?.download?.active ? 500 : state?.session && !state.session.ready ? 1000 : 2500);
  }
}

drawChat();
refresh();
