"use strict";
const $ = (id) => document.getElementById(id),
  key = location.hash.slice(1) || sessionStorage.getItem("yourlm") || "";
if (location.hash) {
  sessionStorage.setItem("yourlm", key);
  history.replaceState(null, "", location.pathname);
}
let state,
  messages = [],
  pending = false,
  stream = null,
  scanId = 0,
  offered = "",
  lastSession = "";
const gib = (n) =>
  (Number(n || 0) / 1073741824).toLocaleString("pl-PL", {
    maximumFractionDigits: 1,
  });
function note(s) {
  $("notice").textContent = s;
  $("notice").hidden = !s;
}
async function api(op, params) {
  let res = await fetch("/api/" + op, {
    method: params ? "POST" : "GET",
    headers: { "X-YourLM": key, "Content-Type": "application/json" },
    body: params ? JSON.stringify(params) : undefined,
  });
  if (!res.ok) throw Error((await res.text()).trim());
  return res.json();
}
function el(n, t) {
  let x = document.createElement(n);
  x.textContent = t;
  return x;
}
function options(id, arr, first) {
  let s = $(id),
    vals = JSON.stringify(arr);
  if (s.dataset.vals === vals) return;
  let prev = s.value;
  s.replaceChildren();
  if (first) s.add(new Option(first, ""));
  for (let v of arr) s.add(new Option(v, v));
  s.dataset.vals = vals;
  if (arr.includes(prev)) s.value = prev;
}
function node(name, role, meta, address) {
  let row = el("div", "");
  row.className = "node";
  let icon = el("span", address ? "RPC" : "CPU");
  icon.className = "icon";
  let d = el("div", "");
  d.append(el("b", name), el("small", meta));
  row.append(icon, d, el("em", role));
  if (address) {
    let b = el("button", "Odłącz");
    b.className = "light";
    b.disabled = !!state.session;
    b.onclick = () => act("remove", { address });
    row.append(b);
  }
  return row;
}
function render(s) {
  state = s;
  let nodes = s.nodes || [],
    sess = s.session,
    sz = nodes.reduce((a, n) => a + n.total, s.total),
    cap = nodes.reduce((a, n) => a + n.budget, s.budget);
  $("count").textContent = 1 + nodes.length;
  $("mode").textContent = s.offer
    ? "Tryb workera"
    : nodes.length
      ? "Klaster hybrydowy"
      : "Host lokalny";
  $("topdesc").textContent = nodes.length
    ? "Podział warstw CPU przez szyfrowane połączenia w LAN."
    : "Obliczenia na tym urządzeniu, bez usług chmurowych.";
  $("ram").textContent = gib(sz) + " GiB";
  $("capacity").textContent = gib(cap) + " GiB";
  $("nodes").replaceChildren(
    node(
      s.host,
      "HOST",
      `${s.os} · ${s.cores} wątków · ${gib(s.total)} GiB RAM`,
    ),
    ...nodes.map((n) =>
      node(
        n.name,
        "WORKER",
        `${n.address} · ${n.cores} wątków · ${gib(n.total)} GiB RAM`,
        n.address,
      ),
    ),
  );
  $("add").disabled = !!sess || !!s.offer;
  $("offer").hidden = s.os !== "windows";
  options("workerIP", s.ips.slice(1));
  $("offerBtn").textContent = s.offer ? "Zatrzymaj worker" : "Uruchom worker";
  $("offerCode").hidden = !s.offer;
  $("offerCode").value = s.offer;
  if (s.offer !== offered) {
    offered = s.offer;
    $("winQR").replaceChildren();
    if (offered) {
      let qr = qrcode(0, "M");
      qr.addData(offered);
      qr.make();
      $("winQR").innerHTML = qr.createSvgTag(4, 16);
    }
  }
  $("engine").textContent = s.runtime
    ? "b12562 · zainstalowany, gotowy do pracy offline"
    : s.installing
      ? "Pobieranie i weryfikacja SHA-256…"
      : "Brak silnika. Pobierz go raz z GitHub (~25 MB).";
  $("install").hidden = s.runtime;
  $("install").disabled = s.installing || s.os !== "windows";
  $("selected").textContent = s.model.name
    ? `${s.model.name} · ${gib(s.model.size)} GiB · ${s.model.layers} warstw`
    : "Nie wybrano pliku.";
  $("advice").textContent = s.advice;
  $("pick").hidden = s.os !== "windows";
  $("pick").disabled = $("manual").disabled = !!sess;
  options("library", s.models || [], "Wybierz z biblioteki…");
  $("library").hidden = !s.models.length;
  $("library").disabled = !!sess;
  $("libdir").textContent = "Folder modeli: " + s.library;
  options("bind", s.ips);
  $("bind").disabled = $("port").disabled = !!sess;
  $("run").textContent = sess ? "Zatrzymaj model" : "Uruchom model";
  $("run").disabled = !sess && (!s.model.name || !s.runtime || !!s.offer);
  $("sessionInfo").hidden = !sess;
  if (sess) {
    $("endpoint").textContent = sess.url;
    $("runstate").textContent =
      (sess.ready ? "Gotowy" : "Ładowanie modelu…") +
      ` · ${sess.layers} warstw na workerach`;
  }
  $("chatstatus").textContent = sess?.ready
    ? sess.name
    : sess
      ? "Trwa ładowanie modelu."
      : "Wybierz i uruchom model w zakładce Modele i API.";
  $("send").querySelector("button").disabled = !sess?.ready || pending;
  if (lastSession && lastSession !== sess?.key) {
    messages = [];
    $("speed").textContent = "";
    drawChat();
  }
  lastSession = sess?.key || "";
  if (s.last && s.last !== render.last) note(s.last);
  render.last = s.last;
}
async function act(op, data = {}, dlg) {
  try {
    note("");
    if (dlg) dlg.querySelector(".error").textContent = "";
    let s = await api(op, data);
    render(s);
    if (dlg) dlg.close();
    return true;
  } catch (e) {
    if (dlg) dlg.querySelector(".error").textContent = e.message;
    else note(e.message);
    return false;
  }
}
const headings = {
  cluster: [
    "Centrum klastra",
    "Zarządzaj pamięcią, modelami i urządzeniami w swojej sieci.",
    "KLASTER",
  ],
  model: [
    "Modele i API",
    "Wczytaj własne wagi GGUF i udostępnij lokalny endpoint.",
    "RUNTIME",
  ],
  chat: [
    "Czat lokalny",
    "Rozmawiaj bezpośrednio z modelem uruchomionym na Twoim sprzęcie.",
    "CZAT",
  ],
};
document.querySelectorAll("nav button").forEach(
  (b) =>
    (b.onclick = () => {
      for (let t of document.querySelectorAll(".tab"))
        t.hidden = t.id !== b.dataset.tab;
      document
        .querySelectorAll("nav button")
        .forEach((v) => v.classList.toggle("sel", v === b));
      let [h, p, c] = headings[b.dataset.tab];
      $("title").textContent = h;
      $("sub").textContent = p;
      $("crumb").textContent = c;
    }),
);
$("add").onclick = () => {
  $("pair").showModal();
  $("pair").querySelector(".error").textContent = "";
};
$("manual").onclick = () => $("pathDlg").showModal();
document
  .querySelectorAll(".close")
  .forEach((b) => (b.onclick = () => b.closest("dialog").close()));
$("pairBtn").onclick = () => act("pair", { code: $("code").value }, $("pair"));
$("choose").onclick = () =>
  act("model", { path: $("path").value }, $("pathDlg"));
$("library").onchange = (e) =>
  e.target.value && act("model", { path: e.target.value });
$("pick").onclick = () => act("pick");
$("install").onclick = () => act("install");
$("offerBtn").onclick = () =>
  act(state.offer ? "unoffer" : "offer", { bind: $("workerIP").value });
$("run").onclick = () => {
  if (
    !state.session &&
    $("bind").value !== "127.0.0.1" &&
    !confirm("Udostępnić API przez HTTP w zaufanej sieci prywatnej?")
  )
    return;
  act(state.session ? "stop" : "start", {
    bind: $("bind").value,
    port: Number($("port").value),
  });
};
function stopScan() {
  scanId++;
  stream?.getTracks().forEach((t) => t.stop());
  stream = null;
  $("video").hidden = true;
  $("scan").disabled = false;
}
$("pair").onclose = () => {
  stopScan();
  $("code").value = "";
};
$("scan").onclick = async () => {
  let id = ++scanId;
  $("scan").disabled = true;
  try {
    let cam = await navigator.mediaDevices.getUserMedia({
      video: { width: 640, facingMode: { ideal: "environment" } },
    });
    if (scanId !== id) {
      cam.getTracks().forEach((t) => t.stop());
      return;
    }
    stream = cam;
    let v = $("video");
    v.srcObject = cam;
    v.hidden = false;
    await v.play();
    let c = document.createElement("canvas"),
      ctx = c.getContext("2d", { willReadFrequently: true });
    function scan() {
      if (scanId !== id) return;
      if (v.videoWidth) {
        c.width = v.videoWidth;
        c.height = v.videoHeight;
        ctx.drawImage(v, 0, 0);
        let img = ctx.getImageData(0, 0, c.width, c.height),
          qr = jsQR(img.data, img.width, img.height);
        if (qr?.data.startsWith("YLM1|")) {
          $("code").value = qr.data;
          stopScan();
          $("pairBtn").click();
          return;
        }
      }
      setTimeout(scan, 180);
    }
    scan();
  } catch (e) {
    stopScan();
    $("pair").querySelector(".error").textContent =
      "Kamera niedostępna. Wklej kod sesji.";
  }
};
async function copy(s) {
  try {
    await navigator.clipboard.writeText(s);
    note("Skopiowano do schowka.");
  } catch {
    note("Schowek niedostępny.");
  }
}
$("copyKey").onclick = () => copy(state.session.key);
$("copyCfg").onclick = () =>
  copy(
    `name: YourLM\nversion: 1.0.0\nschema: v1\nmodels:\n  - name: YourLM lokalny\n    provider: openai\n    model: yourlm\n    apiBase: ${state.session.url}\n    apiKey: ${state.session.key}\n    roles: [chat, edit, apply]`,
  );
function drawChat() {
  let list = $("messages");
  list.replaceChildren(
    ...messages.map((m) => {
      let d = el("div", "");
      d.className = "bubble";
      d.append(
        el("b", m.role === "user" ? "TY" : "YOUR LM"),
        document.createTextNode(m.content),
      );
      return d;
    }),
  );
}
$("clear").onclick = () => {
  if (!pending) {
    messages = [];
    drawChat();
  }
};
$("send").onsubmit = async (e) => {
  e.preventDefault();
  let p = $("prompt").value.trim();
  if (!p || pending) return;
  pending = true;
  let session = state.session?.key;
  messages.push({ role: "user", content: p });
  $("prompt").value = "";
  drawChat();
  render(state);
  try {
    let j = await api("chat", { messages });
    if (session === state.session?.key) {
      let answer = j.choices?.[0]?.message?.content;
      if (typeof answer !== "string") throw Error("Brak tekstu odpowiedzi");
      messages.push({ role: "assistant", content: answer });
      let rate = j.timings?.predicted_per_second;
      if (Number.isFinite(rate))
        $("speed").textContent =
          "Ostatnia odpowiedź: " + rate.toFixed(1) + " tokenów/s";
    }
  } catch (e) {
    note(e.message);
    if (session === state.session?.key) {
      messages.pop();
      $("prompt").value = p;
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
  } catch (e) {
    note(e.message);
  } finally {
    setTimeout(refresh, 3000);
  }
}
refresh();
