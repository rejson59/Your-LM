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
let generationController = null;
let stream = null;
let scanId = 0;
let stopTokenWarning = false;
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

async function streamChat(params, signal, onText) {
  const response = await fetch("/api/chat", {
    method: "POST",
    headers: {
      "X-YourLM": key,
      "Content-Type": "application/json",
      Accept: "text/event-stream",
    },
    body: JSON.stringify(params),
    signal,
  });
  if (!response.ok) throw Error((await response.text()).trim());
  if (!response.body) throw Error("Przeglądarka nie udostępniła strumienia odpowiedzi");

  if (!response.headers.get("Content-Type")?.includes("text/event-stream")) {
    const result = await response.json();
    const text = result.choices?.[0]?.message?.content;
    if (typeof text !== "string") throw Error("Silnik nie zwrócił tekstu odpowiedzi");
    onText(text);
    return result;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffered = "";
  let timings;
  const consume = (line) => {
    if (!line.startsWith("data:")) return false;
    const data = line.slice(5).trim();
    if (!data || data === "[DONE]") return data === "[DONE]";
    let event;
    try {
      event = JSON.parse(data);
    } catch {
      throw Error("Silnik zwrócił uszkodzony fragment odpowiedzi");
    }
    if (event.error) throw Error(event.error.message || String(event.error));
    const text = event.choices?.[0]?.delta?.content;
    if (typeof text === "string" && text) onText(text);
    if (event.timings) timings = event.timings;
    return false;
  };

  let done = false;
  while (!done) {
    const part = await reader.read();
    buffered += decoder.decode(part.value || new Uint8Array(), { stream: !part.done });
    const lines = buffered.split(/\r?\n/);
    buffered = lines.pop() || "";
    for (const line of lines) {
      if (consume(line)) {
        done = true;
        break;
      }
    }
    if (part.done) break;
  }
  if (buffered) consume(buffered);
  return { timings };
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
    if (download.sha256) pieces.push(`SHA-256 ${download.sha256}`);
    if (download.error) pieces.push(download.error);
    $("hfDownloadMeta").textContent = pieces.filter(Boolean).join(" · ");
    $("hfCancel").hidden = !download.active;
    $("hfCancel").disabled = !download.active;
    $("hfRetry").hidden = download.active || download.complete || !(download.canceled || download.error);
    $("hfRetry").disabled = download.active;
    $("hfRetry").dataset.repo = download.repo || "";
    $("hfRetry").dataset.file = download.file || "";
  }

  const curated = (hf.curated || [])[0] || {};
  const golden = $("goldenDownload");
  const goldenDownload = download.recipeId === curated.id || download.recipeId === "qwen36-35b-a3b-abliterated-balanced";
  golden.disabled = Boolean(state?.session || download.active);
  golden.textContent = goldenDownload && download.active
    ? "Budowanie modelu Balanced…"
    : goldenDownload && download.complete
      ? "Model Balanced gotowy i wybrany"
      : goldenDownload && (download.canceled || download.error)
        ? "Wznów model Balanced"
        : "Zbuduj i wybierz model Balanced";
  $("goldenStatus").textContent = goldenDownload
    ? [download.message, download.sha256 ? `SHA-256: ${download.sha256}` : ""].filter(Boolean).join(" · ")
    : curated.description || "Wagi nie są dołączone do aplikacji; zostaną złożone lokalnie z przypiętych źródeł.";

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

function diagnosticFact(label, value) {
  const row = el("div", "");
  row.className = "factRow";
  row.append(el("b", label), el("span", value || "—"));
  return row;
}

function renderStorage(storage = {}) {
  const migration = storage.migration || {};
  const status = migration.status || (storage.warning ? "fallback" : "ready");
  const states = {
    checking: { badge: "KONTROLA", tone: "working", source: "chronione", destination: "sprawdzanie" },
    copying: { badge: "WZNAWIANIE", tone: "working", source: "aktywne i chronione", destination: "kopia etapowa" },
    recovered: { badge: "ODZYSKANO", tone: "success", source: "zweryfikowane", destination: "aktywne" },
    completed: { badge: "GOTOWE", tone: "success", source: "zweryfikowane", destination: "aktywne" },
    ready: { badge: "D: AKTYWNE", tone: "success", source: migration.sourceRetained ? "kopia zachowana" : "przeniesione", destination: "aktywne" },
    retained: { badge: "D: AKTYWNE", tone: "notice", source: "kopia zachowana", destination: "aktywne" },
    fallback: { badge: "C: BEZPIECZNE", tone: "warning", source: "aktywne i nietknięte", destination: "nieopublikowane" },
    busy: { badge: "DRUGIE OKNO", tone: "warning", source: "zablokowane bezpiecznie", destination: "migracja trwa" },
    unavailable: { badge: "C: AKTYWNE", tone: "warning", source: "aktywne", destination: "niedostępne" },
    custom: { badge: "WŁASNA ŚCIEŻKA", tone: "neutral", source: "bez migracji", destination: "aktywne" },
    local: { badge: "LOKALNIE", tone: "neutral", source: "prywatne", destination: "aktywne" },
  };
  const view = states[status] || states.ready;
  const pathVolume = (path, fallback) => {
    const match = String(path || "").match(/^[a-z]:/i);
    return match ? match[0].toUpperCase() : fallback;
  };
  const sourceLabel = pathVolume(migration.sourcePath, status === "fallback" ? "C:" : "ŹRÓDŁO");
  const destinationLabel = pathVolume(migration.destinationPath || storage.dataPath, "D:");
  const card = document.querySelector(".storageCard");
  card.dataset.state = view.tone;
  $("storageStateIcon").textContent = status === "fallback" || status === "unavailable" ? sourceLabel : destinationLabel;
  $("storageBadge").textContent = view.badge;
  $("storageBadge").className = `storageBadge ${view.tone}`;
  $("storageTitle").textContent = migration.title || "Program, modele i dane";
  $("storageMessage").textContent = storage.warning || migration.detail || storage.message || "Magazyn nie został jeszcze rozpoznany.";
  $("storageMessage").classList.toggle("bad", view.tone === "warning");
  $("storageSourceLabel").textContent = sourceLabel;
  $("storageSourceState").textContent = view.source;
  $("storageDestinationLabel").textContent = destinationLabel;
  $("storageDestinationState").textContent = view.destination;
  $("storageFlow").className = `storageFlow ${view.tone}`;

  const stats = [];
  if (migration.resumedFiles) stats.push(["WZNOWIONO", `${migration.resumedFiles} plik · ${humanBytes(migration.resumedBytes)}`]);
  if (migration.reusedFiles) stats.push(["UŻYTO PONOWNIE", `${migration.reusedFiles} plik · ${humanBytes(migration.reusedBytes)}`]);
  if (migration.recoveredParts) stats.push(["ODRZUCONO", `${migration.recoveredParts} uszkodzona część`]);
  if (migration.replacedFiles) stats.push(["ODŚWIEŻONO", `${migration.replacedFiles} niezgodny plik stagingu`]);
  if (migration.removedEntries) stats.push(["UPRZĄTNIĘTO", `${migration.removedEntries} osierocony wpis stagingu`]);
  if (migration.verifiedFiles) stats.push(["SHA-256", `${migration.verifiedFiles} plik · ${humanBytes(migration.verifiedBytes)}`]);
  if (migration.recoveredLock) stats.push(["BLOKADA", "odzyskana po przerwaniu"]);
  $("storageRecoveryStats").hidden = !stats.length;
  $("storageRecoveryStats").replaceChildren(...stats.map(([label, value]) => {
    const item = el("div", "");
    item.append(el("small", label), el("b", value));
    return item;
  }));
  $("storageAction").hidden = !migration.action;
  $("storageAction").textContent = migration.action || "";
  $("storageTechnical").hidden = !migration.error;
  $("storageError").textContent = migration.error || "";

  const paths = [];
  if (storage.installPath) paths.push(`Program: ${storage.installPath}`);
  if (storage.dataPath) paths.push(`Aktywne modele i dane: ${storage.dataPath}`);
  if (migration.stagingPath && (status === "fallback" || status === "copying")) paths.push(`Etap (nieaktywny): ${migration.stagingPath}`);
  if (migration.sourceRetained && migration.retainedPath && migration.retainedPath !== storage.dataPath) paths.push(`Zachowana kopia: ${migration.retainedPath}`);
  $("storagePaths").replaceChildren(...paths.map((value) => el("span", value)));
  const rows = (storage.drives || []).map((drive) => {
    const row = el("div", "");
    row.className = `driveRow${drive.selected ? " selected" : ""}${drive.recommended ? " recommended" : ""}`;
    const letter = el("span", drive.letter || "DYSK");
    letter.className = "driveLetter";
    const detail = el("div", "");
    const text = el("div", "");
    text.append(
      el("b", [drive.label, drive.fileSystem, drive.type].filter(Boolean).join(" · ") || "Dysk lokalny"),
      el("small", `${humanBytes(drive.free)} wolne z ${humanBytes(drive.total)}${drive.system ? " · systemowy" : ""}`),
    );
    const meter = el("span", "");
    meter.className = "driveMeter";
    const fill = el("i", "");
    fill.style.width = `${drive.total ? Math.max(2, Math.min(100, Number(drive.free) / Number(drive.total) * 100)) : 0}%`;
    meter.append(fill);
    detail.append(text, meter);
    const role = drive.selected ? "AKTYWNY" : drive.recommended ? (status === "fallback" ? "CEL PO NAPRAWIE" : "ZALECANY") : "DOSTĘPNY";
    row.append(letter, detail, el("em", role));
    return row;
  });
  $("storageDrives").replaceChildren(...rows);
}

function phoneDiagnosticRow(phone) {
  const row = el("div", "");
  row.className = "diagnosticItem";
  const heading = el("div", "");
  heading.append(el("b", phone.name || phone.model || "Telefon Android"), el("small", phone.source || "lokalny pomiar"));
  const facts = el("div", "");
  const frequencies = phone.maxFrequenciesKHz || [];
  const maxFrequency = frequencies.length ? Math.max(...frequencies.map(Number)) / 1000000 : 0;
  const soc = [phone.soc, phone.logicalCores ? `${phone.logicalCores} rdzeni` : "", maxFrequency ? `${maxFrequency.toLocaleString("pl-PL", { maximumFractionDigits: 2 })} GHz` : ""].filter(Boolean).join(" · ");
  facts.append(
    el("b", `${phone.android ? `Android ${phone.android}` : "Android"}${phone.sdk ? ` · SDK ${phone.sdk}` : ""}`),
    el("span", soc || "SoC nie został jeszcze odczytany"),
    el("span", `RAM: ${humanBytes(phone.ramTotal)} fizyczne · ${humanBytes(phone.ramAvailable)} dostępne`),
    el("span", `Pamięć: ${humanBytes(phone.storageFree)} wolne z ${humanBytes(phone.storageTotal)} · zapis ${phone.diskWriteBps ? linkRate(phone.diskWriteBps) : "—"} · odczyt ${phone.diskReadBps ? linkRate(phone.diskReadBps) : "—"}`),
    el("span", `ARM dot-product: ${phone.dotProduct ? "potwierdzone" : "niepotwierdzone"}${phone.batteryLevel ? ` · bateria ${phone.batteryLevel}%` : ""}${phone.batteryTempC ? ` · ${Number(phone.batteryTempC).toLocaleString("pl-PL", { maximumFractionDigits: 1 })}°C` : ""}`),
  );
  if (phone.warning) facts.append(el("small", `Uwaga: ${phone.warning}`));
  row.append(heading, facts);
  return row;
}

function diagnosticLinkRow(link) {
  const row = el("div", "");
  row.className = "diagnosticItem";
  const heading = el("div", "");
  heading.append(el("b", link.name || "Worker"), el("small", `Wybrany transport: ${link.transport === "usb" ? "USB/ADB" : "Wi-Fi/LAN"}`));
  const facts = el("div", "");
  const describe = (name, value) => value
    ? `${name}: ↑ ${linkRate(value.uploadBps)} · ↓ ${linkRate(value.downloadBps)} · ping ${(Number(value.latencyMicros || 0) / 1000).toLocaleString("pl-PL", { maximumFractionDigits: 1 })} ms`
    : `${name}: brak pomiaru`;
  facts.append(el("span", describe("Wi-Fi", link.wifi)), el("span", describe("USB", link.usb)));
  if (link.warning) facts.append(el("small", link.warning));
  row.append(heading, facts);
  return row;
}

function renderDiagnostics(diagnostics = {}, storage = {}, session, hf = {}) {
  renderStorage(storage);
  const running = Boolean(diagnostics.running);
  const complete = Boolean(diagnostics.complete);
  const steps = Math.max(1, Number(diagnostics.steps || 6));
  const step = Math.max(0, Math.min(steps, Number(diagnostics.step || 0)));
  const progress = complete ? 100 : running ? Math.max(4, ((step - 1) / steps) * 100) : 0;
  const hasPhone = Boolean(diagnostics.phones?.length);
  $("diagTitle").textContent = running ? "Test urządzeń trwa" : complete ? hasPhone ? "Raport Lenovo + Redmi jest gotowy" : "Raport Lenovo gotowy — Redmi nie wykryto" : diagnostics.error ? "Test wymaga uwagi" : "Gotowy do pełnego testu";
  $("diagMessage").textContent = diagnostics.error || diagnostics.message || "Uruchom lokalny test urządzeń.";
  $("diagProgressFill").style.width = `${progress}%`;
  $("diagProgress").setAttribute("aria-valuenow", String(step));
  $("diagStep").textContent = running || complete ? `Etap ${step} z ${steps}${complete ? " · zakończono" : ""}` : "Nie uruchomiono.";
  $("diagStart").disabled = running || Boolean(session) || Boolean(hf.download?.active);
  $("diagStart").textContent = complete ? "Uruchom test ponownie" : "Uruchom pełny test";
  $("diagCancel").hidden = !running;
  $("diagCancel").disabled = !running;
  $("diagCopy").disabled = !diagnostics.report;

  const host = diagnostics.host || {};
  const hostFacts = [];
  if (host.cpu || host.ramTotal) {
    hostFacts.push(
      diagnosticFact("Urządzenie", [host.manufacturer, host.model].filter(Boolean).join(" ") || "Lenovo / host"),
      diagnosticFact("System", [host.os, host.osVersion, host.architecture].filter(Boolean).join(" · ")),
      diagnosticFact("CPU", `${host.cpu || "—"}${host.physicalCores ? ` · ${host.physicalCores} fizyczne` : ""} · ${host.logicalCores || 0} logiczne${host.maxMHz ? ` · ${host.maxMHz} MHz` : ""}`),
      diagnosticFact("RAM", `${humanBytes(host.ramTotal)} fizyczne · ${humanBytes(host.ramAvailable)} dostępne podczas testu`),
    );
    for (const gpu of host.gpus || []) {
      hostFacts.push(diagnosticFact("GPU", `${gpu.name}${gpu.memoryBytes ? ` · ${humanBytes(gpu.memoryBytes)}` : ""}${gpu.driverVersion ? ` · sterownik ${gpu.driverVersion}` : ""}`));
    }
    for (const disk of host.disks || []) {
      hostFacts.push(diagnosticFact("Dysk fizyczny", `${disk.model}${disk.mediaType ? ` · ${disk.mediaType}` : ""}${disk.interface ? ` · ${disk.interface}` : ""}${disk.size ? ` · ${humanBytes(disk.size)}` : ""}`));
    }
    const benchmark = diagnostics.benchmark || {};
    hostFacts.push(
      diagnosticFact("CPU test", benchmark.cpuHashBps ? `${linkRate(benchmark.cpuHashBps)} SHA-256 · ${benchmark.cpuWorkers} wątków` : "oczekuje"),
      diagnosticFact("Dysk YourLM", benchmark.diskWriteBps ? `zapis ${linkRate(benchmark.diskWriteBps)} · odczyt ciepły ${linkRate(benchmark.diskReadBps)} · próbka ${humanBytes(benchmark.diskBytes)}` : benchmark.warning || "oczekuje"),
    );
    if (host.warning) hostFacts.push(diagnosticFact("Uwaga", host.warning));
  } else {
    hostFacts.push(el("p", "Uruchom test, aby odczytać producenta, model, CPU, fizyczne rdzenie, RAM i GPU."));
  }
  $("diagHost").replaceChildren(...hostFacts);

  const phones = diagnostics.phones || [];
  $("diagPhones").replaceChildren(...(phones.length ? phones.map(phoneDiagnosticRow) : [el("p", running ? "Oczekiwanie na Redmi…" : "Podłącz odblokowany telefon z zaakceptowanym debugowaniem USB albo sparuj działający worker.")]));
  const links = diagnostics.links || [];
  $("diagLinks").replaceChildren(...(links.length ? links.map(diagnosticLinkRow) : [el("p", running ? "Oczekiwanie na test łącza…" : "Po sparowaniu Redmi test porówna opóźnienie oraz transfer w obie strony.")]));

  const model = diagnostics.modelBenchmark || {};
  $("diagModel").disabled = Boolean(model.running || running || !session?.ready);
  $("diagModel").textContent = model.running ? "Test modelu trwa…" : model.complete ? "Powtórz test modelu" : "Testuj działający model";
  $("diagModelText").textContent = model.error
    ? `Test nie powiódł się: ${model.error}`
    : model.complete
      ? `${model.model} · ${Number(model.tokensPerSecond || 0).toLocaleString("pl-PL", { maximumFractionDigits: 2 })} tokena/s · prompt ${Number(model.promptTokensPerSecond || 0).toLocaleString("pl-PL", { maximumFractionDigits: 2 })} tokena/s · ścisła instrukcja: ${model.instructionOK ? "wykonana" : "niewykonana"}.`
      : session?.ready
        ? "Model jest gotowy. Próba nie trafi do historii czatu."
        : "Po pobraniu i uruchomieniu modelu wykonaj krótką próbę. Nie trafia ona do historii czatu.";

  const recommendation = diagnostics.recommendation || {};
  $("autoModel").textContent = recommendation.selectedName || "Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4";
  $("autoReason").textContent = recommendation.reason || "Pełny test sprzętu doprecyzuje decyzję.";
  $("autoMaximum").textContent = recommendation.maximumCandidate || "Qwen3-Coder-Next 80B-A3B Abliterated · IQ3_XS experts-last";
  $("autoMaximumStatus").textContent = recommendation.maximumStatus || "Czeka na pomiar urządzeń.";
  $("autoNext").textContent = recommendation.nextStep || "Najpierw uruchom pełny test.";
  $("autoApply").disabled = !recommendation.readyToApply || Boolean(session || hf.download?.active);
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
  renderDiagnostics(state.diagnostics || {}, state.storage || {}, session, state.huggingFace || {});
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

  const modelPlan = state.modelPlan || {};
  $("engine").textContent = state.runtime
    ? modelPlan.useSmol
      ? "Osobny runtime smolmaxxing jest zgodny z tym modelem i procesorem. Profil zostanie uruchomiony automatycznie."
      : "Wbudowany stabilny silnik jest gotowy. Nie potrzebujesz dodatkowego folderu runtime."
    : "Nie udało się przygotować wbudowanego silnika. Uruchom ponownie YourLM.exe lub pobierz aktualną wersję.";
  document.querySelector(".runtimeStatus").classList.toggle("bad", !state.runtime);
  $("profileCard").hidden = !state.model.name;
  $("profileName").textContent = modelPlan.label || "Profil standardowy";
  $("profileEngine").textContent = modelPlan.engine || "llama.cpp b12562";
  const profileFacts = modelPlan.useSmol
    ? ` Kontekst ${Number(modelPlan.contextSize || 0).toLocaleString("pl-PL")}, pula ${modelPlan.expertPool} ekspertów/warstwę, ${modelPlan.threads} rdzenie obliczeń, ${modelPlan.readers} czytniki UFS, cache ${modelPlan.cacheReady ? "gotowy" : "do zbudowania"}.`
    : "";
  $("profileDescription").textContent = (modelPlan.description || "Uniwersalny profil zgodności.") + profileFacts;
  $("profileWarning").textContent = modelPlan.warning || "";
  $("profileWarning").hidden = !modelPlan.warning;
  $("selected").textContent = state.model.name
    ? `${state.model.name} · ${gib(state.model.size)} GiB · ${state.model.layers} warstw`
    : "Nie wybrano pliku.";
  $("advice").textContent = state.advice || "Po wybraniu pliku pokażemy ilość dostępnej pamięci. Ostateczny podział dobierze llama.cpp przy starcie.";
  $("advice").classList.toggle("warn", Boolean(state.modelWarning));
  $("pick").hidden = state.os !== "windows";
  $("pick").disabled = $("manual").disabled = $("hfBrowse").disabled = Boolean(session);
  $("goldenDownload").disabled = Boolean(session || state.huggingFace?.download?.active);
  renderHuggingFace(state.huggingFace || {});
  options("library", state.models || [], "Wybierz z prywatnej biblioteki…");
  $("library").hidden = !state.models.length;
  $("library").disabled = Boolean(session);
  $("libdir").textContent = "Biblioteka: " + state.library;

  options("bind", state.ips);
  $("bind").disabled = $("port").disabled = Boolean(session);
  $("run").textContent = session ? "Zatrzymaj model" : modelPlan.useSmol ? "Uruchom Qwen3.6 Balanced" : "Uruchom model bez limitu";
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
    const stopTokenIssue = /removing it from EOG list/i.test(engineState.log || "");
    const mmapAdvisory = /tensor overrides to cpu.*mmap/i.test(engineState.log || "");
    $("loadHint").hidden = !loading;
    $("loadHint").textContent = loading
      ? (workers.length
          ? "Pierwszy start rozdziela i przesyła wagi wybranym łączem. Przed startem YourLM porównuje Wi-Fi i dostępny tunel USB/ADB; pokazywana szybkość RPC obejmuje też TLS, zapis cache i pracę telefonu. Kolejne uruchomienia mogą użyć pamięci podręcznej RPC zapisywanej porcjami do 16 MiB. Windows zapisuje migawkę Redmi co 3 sekundy i dołączy ostatnią udaną nawet po utracie listenera telefonu."
          : "Windows odczytuje i mapuje pełny plik GGUF. Czas zależy od jego pełnego rozmiaru, szybkości dysku oraz dostępnego RAM-u i pliku stronicowania.") +
        (stopTokenIssue ? " Ten GGUF ma podejrzane metadane tokenu końca — odpowiedź może nie zatrzymać się sama i dojść do ustawionego limitu." : mmapAdvisory ? " Ostrzeżenie mmap jest informacyjne — silnik nadal ładuje model." : "")
      : "";
    $("engineLogPath").textContent = engineState.path ? `Plik: ${engineState.path}` : "";
    $("engineLog").textContent = engineState.log || "Oczekiwanie na pierwszy komunikat llama.cpp…";
  }
  if (session) $("endpoint").textContent = session.url;

  const sessionKey = session?.key || "";
  if (lastSession !== sessionKey) {
    generationController?.abort();
    messages = [];
    stopTokenWarning = false;
    $("speed").textContent = "";
    drawChat();
  }
  lastSession = sessionKey;
  if (/removing it from EOG list/i.test(engineState.log || "")) {
    stopTokenWarning = true;
  }
  const largeChatModel = Number(state.model?.size || 0) >= 12 * 1073741824;
  $("chatstatus").textContent = pending
    ? "Odpowiedź napływa na żywo. Przycisk „Zatrzymaj” natychmiast kończy generowanie."
    : session?.ready
      ? stopTokenWarning
        ? "Ten GGUF ma podejrzany token końca odpowiedzi i może zawsze dochodzić do limitu. Zacznij od 64 tokenów albo wybierz inny model."
        : largeChatModel
          ? `${session.name} działa, ale to bardzo duży model CPU. USB skraca transfer wag, nie czas obliczania tokenów; zacznij od limitu 64.`
          : `${session.name} działa lokalnie. Odpowiedź będzie pokazywana na żywo.`
      : session
        ? "Model jest ładowany. To może potrwać kilka minut."
        : "Najpierw wybierz i uruchom model w zakładce Model i API.";
  const sendButton = $("send").querySelector("button");
  sendButton.disabled = !session?.ready;
  sendButton.textContent = pending ? "Zatrzymaj ■" : "Wyślij ↑";
  sendButton.setAttribute("aria-label", pending ? "Zatrzymaj generowanie" : "Wyślij");
  sendButton.classList.toggle("stopping", pending);
  $("prompt").disabled = !session?.ready || pending;
  $("maxTokens").disabled = !session?.ready || pending;
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
  document.title = `YourLM · ${{ cluster: "Urządzenia", model: "Model i API", chat: "Czat", phone: "Telefon USB", tests: "Testy i autopilot" }[name]}`;
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
$("diagStart").onclick = () => act("diagnostics-start");
$("diagCancel").onclick = () => act("diagnostics-cancel");
$("diagModel").onclick = () => act("diagnostics-model");
$("diagCopy").onclick = () => copy(state?.diagnostics?.report || "Raport nie jest jeszcze gotowy.");
$("openModelTab").onclick = () => openTab("model");
$("autoApply").onclick = async () => {
  const recommendation = state?.diagnostics?.recommendation || {};
  if (recommendation.selectedId && recommendation.selectedId !== "qwen36-35b-a3b-abliterated-balanced") {
    note("Ta receptura wymaga jeszcze aktualizacji silnika. Raport zachowano do następnego audytu.");
    return;
  }
  if (!confirm("Autopilot pobierze potrzebne zakresy przypiętych wag i zbuduje jeden model Balanced w zarządzanym magazynie YourLM (na dysku D:, gdy spełnia warunki). Pobrane dane pozostaną lokalne. Kontynuować?")) return;
  const ok = await act("hf-golden", { repo: "qwen36-35b-a3b-abliterated-balanced" });
  if (ok) openTab("model");
};
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
$("goldenDownload").onclick = async () => {
  const download = state?.huggingFace?.download || {};
  if (!download.recipeId && !confirm("YourLM pobierze tylko potrzebne zakresy przypiętych Q4_K i Q2_K, a następnie zbuduje jeden model Balanced w prywatnej bibliotece. Proces wymaga około 15–20 GiB transferu i dodatkowego miejsca na cache pagera. Kontynuować?")) return;
  await act("hf-golden", { repo: "qwen36-35b-a3b-abliterated-balanced" });
};
$("hfCancel").onclick = () => act("hf-cancel");
$("hfRetry").onclick = () => {
  const download = state?.huggingFace?.download || {};
  if (download.recipeId) return act("hf-golden", { repo: download.recipeId });
  return act("hf-download", {
    repo: $("hfRetry").dataset.repo,
    file: $("hfRetry").dataset.file,
  });
};
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
      if (message.stopped) {
        const stopped = el("small", "Generowanie zatrzymane — zachowano odebrany fragment.");
        stopped.className = "stoppedReply";
        bubble.append(stopped);
      }
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
  if (pending) {
    generationController?.abort();
    return;
  }
  const prompt = $("prompt").value.trim();
  if (!prompt) return;

  pending = true;
  generationController = new AbortController();
  const session = state.session?.key;
  const userMessage = { role: "user", content: prompt };
  const requestMessages = [...messages, userMessage].map(({ role, content }) => ({ role, content }));
  const assistantMessage = { role: "assistant", content: "", stopped: false };
  messages.push(userMessage, assistantMessage);
  $("prompt").value = "";
  $("speed").textContent = "Oczekiwanie na pierwszy token…";
  drawChat();
  render(state);

  const started = performance.now();
  let fragments = 0;
  let lastProgress = 0;
  try {
    const response = await streamChat(
      {
        messages: requestMessages,
        maxTokens: Number($("maxTokens").value || 64),
      },
      generationController.signal,
      (text) => {
        if (session !== state.session?.key) return;
        assistantMessage.content += text;
        fragments++;
        const now = performance.now();
        if (now - lastProgress >= 500) {
          const seconds = Math.max(0.1, (now - started) / 1000);
          $("speed").textContent = `Odpowiedź na żywo · odebrano ${fragments} fragmentów · około ${(fragments / seconds).toFixed(1)}/s`;
          lastProgress = now;
        }
        drawChat();
      },
    );
    if (session === state.session?.key && !assistantMessage.content) {
      throw Error("Silnik zakończył generowanie bez tekstu odpowiedzi");
    }
    const rate = response.timings?.predicted_per_second;
    if (Number.isFinite(rate)) {
      $("speed").textContent = `Ostatnia odpowiedź: ${rate.toFixed(2)} tokenów/s`;
    } else {
      const seconds = Math.max(0.1, (performance.now() - started) / 1000);
      $("speed").textContent = `Odpowiedź zakończona po ${elapsed(Math.round(seconds))}.`;
    }
  } catch (error) {
    if (error.name === "AbortError" || generationController?.signal.aborted) {
      assistantMessage.stopped = true;
      if (!assistantMessage.content) {
        const index = messages.indexOf(assistantMessage);
        if (index >= 0) messages.splice(index, 1);
      }
      if (session === state.session?.key) {
        $("speed").textContent = "Generowanie zatrzymane przez użytkownika.";
      }
    } else {
      note(error.message);
      for (const message of [assistantMessage, userMessage]) {
        const index = messages.indexOf(message);
        if (index >= 0) messages.splice(index, 1);
      }
      if (session === state.session?.key) $("prompt").value = prompt;
    }
  } finally {
    generationController = null;
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
    setTimeout(refresh, state?.huggingFace?.download?.active || state?.diagnostics?.running || state?.diagnostics?.modelBenchmark?.running ? 500 : state?.session && !state.session.ready ? 1000 : 2500);
  }
}

drawChat();
refresh();
