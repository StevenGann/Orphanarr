"use strict";

// Orphanarr UI. No framework, no build step: the API is the seam, and
// DESIGN D2 records that flipping this choice changes nothing else.
//
// Everything is built with createElement rather than innerHTML. Names here
// come from torrents — hostile input by definition — and one innerHTML on a
// torrent name is a stored XSS in a tool that has broad filesystem access.

const KEY = new URLSearchParams(location.search).get("apikey") || "";

async function api(path, opts) {
  const o = Object.assign({ headers: {} }, opts || {});
  if (KEY) o.headers["X-Api-Key"] = KEY;
  if (o.body) o.headers["Content-Type"] = "application/json";
  const r = await fetch("/api/v1" + path, o);
  const text = await r.text();
  let body = {};
  try { body = text ? JSON.parse(text) : {}; } catch (_) { body = { error: text }; }
  if (!r.ok) throw new Error(body.error || "HTTP " + r.status);
  return body;
}

const $ = (s) => document.querySelector(s);

function el(tag, opts, ...kids) {
  const n = document.createElement(tag);
  if (opts && typeof opts === "object" && !(opts instanceof Node)) {
    for (const [k, v] of Object.entries(opts)) {
      if (k === "class") n.className = v;
      else if (k === "text") n.textContent = String(v);
      else if (k === "html") throw new Error("innerHTML is not used here on purpose");
      else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
      else if (v !== null && v !== undefined && v !== false) n.setAttribute(k, v === true ? "" : String(v));
    }
  } else if (opts !== null && opts !== undefined) {
    kids.unshift(opts);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    n.appendChild(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return n;
}

function bytes(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + " B";
  const u = ["KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = -1;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(1) + " " + u[i];
}

function ago(ts) {
  if (!ts) return "—";
  return String(ts).replace("T", " ").slice(0, 19) + "Z";
}

function toast(msg, kind) {
  const t = el("div", { class: "toast " + (kind || "") , text: msg });
  $("#toasts").appendChild(t);
  setTimeout(() => t.remove(), 6000);
}

function confirmBox(title, lines, onYes, danger) {
  const back = el("div", { class: "modal-back" });
  const box = el("div", { class: "modal" },
    el("h3", { text: title }),
    ...lines.map((l) => el("p", { class: "muted", text: l })),
    el("div", { class: "row" },
      el("button", { class: "action" + (danger ? " danger" : ""), onclick: async () => {
        back.remove();
        await onYes();
      }}, "Yes, do it"),
      el("button", { class: "ghost", onclick: () => back.remove() }, "Cancel")));
  back.appendChild(box);
  back.addEventListener("click", (e) => { if (e.target === back) back.remove(); });
  document.body.appendChild(back);
}

// ---------------------------------------------------------------- navigation

const TABS = ["dash", "orphans", "review", "libraries", "clients", "settings"];
let current = "dash";

function show(tab) {
  current = tab;
  document.querySelectorAll("nav button").forEach((b) =>
    b.classList.toggle("on", b.dataset.tab === tab));
  TABS.forEach((t) => $("#" + t).classList.toggle("on", t === tab));
  render();
}

// ------------------------------------------------------------------- state

let STATE = { status: null, plans: [], orphans: [], clients: [], libraries: [] };

const MEDIA_TYPES = ["movie", "tv", "music", "audiobook", "comic", "ebook", "rom"];

async function refreshAll() {
  try {
    STATE.status = await api("/system/status");
  } catch (e) {
    $("#banners").replaceChildren(banner("bad", "Cannot reach the API", e.message));
    return;
  }
  const [plans, orphans, clients, libraries] = await Promise.all([
    api("/plans").catch(() => ({ plans: [] })),
    api("/orphans").catch(() => ({ orphans: [] })),
    api("/clients").catch(() => ({ clients: [] })),
    api("/libraries").catch(() => ({ libraries: [] })),
  ]);
  STATE.plans = plans.plans || [];
  STATE.orphans = orphans.orphans || [];
  STATE.clients = clients.clients || [];
  STATE.libraries = libraries.libraries || [];
  render();
}

function banner(kind, title, body) {
  return el("div", { class: "banner " + kind },
    el("div", null, el("strong", { text: title }), body ? el("div", { class: "muted", text: body }) : null));
}

function render() {
  const st = STATE.status;
  if (!st) return;

  $("#ver").textContent = st.version || "";

  const bs = [];
  if (st.dry_run) {
    bs.push(banner("dry", "Dry run is ON — nothing will be written.",
      "Plans are produced and shown in full. This is the shipped default and stays on " +
      "until you turn it off in Settings. Executing a plan is refused while it is on."));
  }
  (st.problems || []).forEach((p) => bs.push(banner("bad", "Configuration problem", p)));
  $("#banners").replaceChildren(...bs);

  if (current === "dash") renderDash();
  if (current === "orphans") renderOrphans();
  if (current === "review") renderReview();
  if (current === "libraries") renderLibraries();
  if (current === "clients") renderClients();
}

// --------------------------------------------------------------- dashboard

function renderDash() {
  const st = STATE.status;
  const c = st.counts || {};
  const drafts = STATE.plans.filter((p) => p.status === "draft").length;

  $("#dashstats").replaceChildren(
    stat(c.orphans || 0, "Orphans tracked"),
    stat(drafts, "Plans awaiting review"),
    stat(c.plans_done || 0, "Plans executed"),
    stat(c.unknown || 0, "Unclassified"));

  const entries = Object.entries(st.skip_reasons || {}).sort((a, b) => b[1] - a[1]);
  const rows = entries.map(([code, n]) =>
    el("tr", null, el("td", null, el("code", { text: code })), el("td", { class: "num", text: n })));
  $("#skips tbody").replaceChildren(...(rows.length ? rows : [emptyRow(2, "Nothing skipped yet.")]));
}

function stat(n, label) {
  return el("div", { class: "stat" }, el("div", { class: "n", text: n }), el("div", { class: "l", text: label }));
}

function emptyRow(cols, msg) {
  const td = el("td", { class: "empty", colspan: cols, text: msg });
  return el("tr", null, td);
}

// ----------------------------------------------------------------- orphans

function renderOrphans() {
  const rows = STATE.orphans.map((o) => {
    const conf = el("td", { class: "num" },
      el("span", { class: "pill " + (o.score >= 0.85 ? "good" : "warn"),
                   text: (o.score || 0).toFixed(2) }));
    return el("tr", null,
      el("td", null, el("div", { text: o.name }),
        el("div", { class: "muted mono small", text: o.client_name + " · " + o.external_id.slice(0, 12) })),
      el("td", null, el("span", { class: "pill", text: o.media_type || "unknown" })),
      conf,
      el("td", { class: "num", text: bytes(o.size_bytes) }),
      el("td", null,
        el("button", { class: "ghost small", onclick: () => explain(o.id) }, "Why?"),
        el("button", { class: "ghost small", onclick: () => ignoreOrphan(o.id) }, "Ignore")));
  });
  $("#orphantbl tbody").replaceChildren(...(rows.length ? rows : [emptyRow(5, "No orphans found yet. Run a scan.")]));
}

async function explain(id) {
  let o;
  try { o = await api("/orphans/" + id + "/explain"); }
  catch (e) { return toast(e.message, "bad"); }

  const signals = (o.signals || []).map((s) =>
    el("tr", null,
      el("td", null, el("code", { text: s.Name })),
      el("td", null, el("span", { class: "pill", text: s.Type })),
      el("td", { class: "num", text: (s.Weight || 0).toFixed(2) }),
      el("td", { class: "muted", text: s.Detail || "" })));

  const back = el("div", { class: "modal-back" });
  back.appendChild(el("div", { class: "modal wide" },
    el("h3", { text: o.name }),
    el("p", { class: "muted", text:
      "Classified " + (o.media_type || "unknown") +
      " at " + (o.score || 0).toFixed(2) +
      ", parse confidence " + (o.parse_confidence || 0).toFixed(2) +
      (o.reason ? " — " + o.reason : "") }),
    el("table", { class: "sub" },
      el("thead", null, el("tr", null,
        el("th", { text: "Signal" }), el("th", { text: "Says" }),
        el("th", { class: "num", text: "Weight" }), el("th", { text: "Detail" }))),
      el("tbody", null, ...(signals.length ? signals : [emptyRow(4, "No signals recorded.")]))),
    el("div", { class: "row" },
      el("button", { class: "ghost", onclick: () => back.remove() }, "Close"))));
  back.addEventListener("click", (e) => { if (e.target === back) back.remove(); });
  document.body.appendChild(back);
}

async function ignoreOrphan(id) {
  try {
    await api("/orphans/" + id + "/ignore", { method: "POST" });
    toast("Ignored. It will be skipped on future scans.");
    refreshAll();
  } catch (e) { toast(e.message, "bad"); }
}

// ------------------------------------------------------------------ review

function renderReview() {
  const wrap = $("#reviewlist");
  if (!STATE.plans.length) {
    wrap.replaceChildren(el("div", { class: "empty", text: "No plans yet. Run a scan." }));
    return;
  }
  wrap.replaceChildren(...STATE.plans.map(planCard));
}

function planCard(p) {
  const statusClass = { done: "good", partial: "warn", failed: "bad",
                        blocked: "bad", undone: "", draft: "" }[p.status] || "";

  const head = el("div", { class: "planhead" },
    el("div", null,
      el("strong", { text: p.orphan_name || ("plan " + p.id) }),
      el("div", { class: "muted small" },
        el("span", { class: "pill", text: p.media_type }), " ",
        el("span", { class: "pill " + statusClass, text: p.status }), " ",
        bytes(p.copy_bytes + p.link_bytes) + " to copy")),
    el("div", { class: "row" },
      el("button", { class: "ghost small", onclick: (e) => togglePlan(e, p.id) }, "Files"),
      p.status === "draft"
        ? el("button", { class: "action small", onclick: () => executePlan(p) }, "Execute")
        : null,
      (p.status === "done" || p.status === "partial" || p.status === "failed")
        ? el("button", { class: "ghost small danger", onclick: () => undoPlan(p) }, "Undo")
        : null));

  const kids = [head];
  if (p.needs_review && p.review_reason) {
    kids.push(el("div", { class: "reviewnote" },
      el("strong", { text: "Needs review: " }), p.review_reason));
  }
  if (p.error) {
    kids.push(el("div", { class: "reviewnote bad" }, el("strong", { text: "Error: " }), p.error));
  }
  const warns = Array.isArray(p.warnings) ? p.warnings : [];
  warns.forEach((w) => kids.push(el("div", { class: "reviewnote warn" },
    el("strong", { text: "Sanitised: " }), w)));

  kids.push(el("div", { class: "steps", id: "steps-" + p.id }));
  return el("div", { class: "card plan" }, ...kids);
}

async function togglePlan(e, id) {
  const box = $("#steps-" + id);
  if (box.childElementCount) { box.replaceChildren(); return; }

  let p;
  try { p = await api("/plans/" + id); }
  catch (err) { return toast(err.message, "bad"); }

  const rows = (p.steps || []).map((s) =>
    el("tr", null,
      el("td", { class: "mono small" }, el("div", { class: "muted", text: s.src_path }),
        el("div", null, "→ " + s.dst_path)),
      el("td", { class: "num", text: bytes(s.bytes) }),
      el("td", null, el("span", { class: "pill " + (s.status === "done" ? "good" : ""),
                                  text: s.method_actual || s.method }))));
  box.replaceChildren(el("table", { class: "sub" },
    el("thead", null, el("tr", null,
      el("th", { text: "Source → destination" }),
      el("th", { class: "num", text: "Size" }),
      el("th", { text: "Method" }))),
    el("tbody", null, ...(rows.length ? rows : [emptyRow(3, "No steps.")]))));
}

function executePlan(p) {
  const dry = STATE.status && STATE.status.dry_run;
  const lines = [
    (p.orphan_name || "This plan") + " — " + bytes(p.copy_bytes + p.link_bytes) + " will be copied.",
    "Sources are never modified, moved or deleted. Nothing existing is overwritten.",
    "This can be undone: Undo removes exactly what was created.",
  ];
  if (dry) {
    lines.unshift("Dry run is ON, so this will be REFUSED. Turn it off in Settings first.");
  }
  if (p.needs_review && p.review_reason) {
    lines.push("This plan was flagged for review: " + p.review_reason);
  }
  confirmBox("Execute this plan?", lines, async () => {
    try {
      await api("/plans/" + p.id + "/execute", { method: "POST" });
      toast("Executed.");
    } catch (e) {
      toast(e.message, "bad");
    }
    refreshAll();
  });
}

function undoPlan(p) {
  confirmBox("Undo this plan?", [
    "This removes only the files Orphanarr created, in reverse order, plus any " +
    "directories it made that are now empty.",
    "Your downloads are untouched — they always were.",
  ], async () => {
    try {
      await api("/plans/" + p.id + "/undo", { method: "POST" });
      toast("Undone.");
    } catch (e) { toast(e.message, "bad"); }
    refreshAll();
  }, true);
}

// --------------------------------------------------------------- libraries

function renderLibraries() {
  const byType = {};
  STATE.libraries.forEach((l) => { byType[l.media_type] = l; });
  const statusByType = {};
  (STATE.status.libraries || []).forEach((l) => { statusByType[l.type] = l; });

  const rows = MEDIA_TYPES.map((t) => {
    const l = byType[t] || { media_type: t, root: "", enabled: false, strict_names: false, oneshots_dir: "" };
    const st = statusByType[t];

    const root = el("input", { type: "text", value: l.root, placeholder: "/data/media/" + t });
    const enabled = el("input", { type: "checkbox" });
    enabled.checked = !!l.enabled;
    const strict = el("input", { type: "checkbox" });
    strict.checked = !!l.strict_names;
    const oneshots = el("input", { type: "text", value: l.oneshots_dir || "",
                                   placeholder: t === "comic" || t === "ebook" ? "_oneshots" : "n/a" });
    if (t !== "comic" && t !== "ebook") oneshots.disabled = true;

    let badge = el("span", { class: "muted small", text: "not configured" });
    if (st) {
      const good = st.hardlinks === "available";
      badge = el("span", { class: "pill " + (good ? "good" : "warn"), text: st.hardlinks });
    }

    return el("tr", null,
      el("td", null, el("strong", { text: t })),
      el("td", null, root),
      el("td", { class: "num", text: st ? bytes(st.free_bytes) : "—" }),
      el("td", null, badge, st && st.hardlink_detail
        ? el("div", { class: "muted small", text: st.hardlink_detail }) : null),
      el("td", { class: "center" }, enabled),
      el("td", { class: "center" }, strict),
      el("td", null, oneshots),
      el("td", null, el("button", { class: "ghost small", onclick: async () => {
        try {
          await api("/libraries", { method: "POST", body: JSON.stringify({
            media_type: t, root: root.value.trim(), enabled: enabled.checked,
            strict_names: strict.checked, oneshots_dir: oneshots.value.trim(),
          })});
          toast(t + " library saved.");
          refreshAll();
        } catch (e) { toast(e.message, "bad"); }
      }}, "Save")));
  });

  $("#libtbl tbody").replaceChildren(...rows);
}

// ----------------------------------------------------------------- clients

function renderClients() {
  const statusById = {};
  (STATE.status.clients || []).forEach((c) => { statusById[c.id] = c; });

  const cards = STATE.clients.map((row) => clientCard(row.client, row.has_secret, statusById[row.client.id]));
  cards.push(clientCard({ id: 0, name: "", kind: "qbittorrent", base_url: "", username: "",
                          enabled: true, mappings: [] }, false, null, true));
  $("#clientlist").replaceChildren(...cards);
}

function clientCard(c, hasSecret, st, isNew) {
  const name = el("input", { type: "text", value: c.name, placeholder: "qBittorrent" });
  const url = el("input", { type: "text", value: c.base_url, placeholder: "http://qbittorrent:8080" });
  const user = el("input", { type: "text", value: c.username, placeholder: "admin" });
  const pass = el("input", { type: "password", placeholder: hasSecret ? "unchanged" : "password" });
  const key = el("input", { type: "password", placeholder: hasSecret ? "unchanged" : "API key (qBittorrent 5.2+)" });
  const enabled = el("input", { type: "checkbox" });
  enabled.checked = !!c.enabled;

  const mapBox = el("div", { class: "maps" });
  const addMapRow = (m) => {
    const remote = el("input", { type: "text", value: (m && m.remote) || "", placeholder: "/downloads" });
    const local = el("input", { type: "text", value: (m && m.local) || "", placeholder: "/data/torrents" });
    const row = el("div", { class: "maprow" }, remote, el("span", { class: "muted", text: "→" }), local,
      el("button", { class: "ghost small", onclick: () => row.remove() }, "×"));
    row._get = () => ({ remote: remote.value.trim(), local: local.value.trim() });
    mapBox.appendChild(row);
  };
  (c.mappings || []).forEach(addMapRow);

  const collect = () => ({
    id: c.id, name: name.value.trim(), kind: "qbittorrent",
    base_url: url.value.trim(), username: user.value.trim(),
    password: pass.value, api_key: key.value, enabled: enabled.checked,
    mappings: Array.from(mapBox.children).map((r) => r._get()).filter((m) => m.remote && m.local),
  });

  const result = el("div", { class: "testresult" });

  const head = el("div", { class: "planhead" },
    el("strong", { text: isNew ? "Add a client" : (c.name || "client " + c.id) }),
    st ? el("span", { class: "pill " + (st.scannable ? "good" : "bad"),
                      text: st.scannable ? (st.app_version || "ok") : "refused" }) : null);

  const kids = [head];
  if (st && !st.scannable && st.refusal) {
    kids.push(el("div", { class: "reviewnote bad" }, el("strong", { text: "Not scanned: " }), st.refusal));
  }

  kids.push(el("div", { class: "form2" },
    field("Name", name), field("URL", url),
    field("Username", user), field("Password", pass),
    field("API key (optional)", key), field("Enabled", enabled)));

  kids.push(el("div", { class: "sub-h" }, "Path mappings",
    el("div", { class: "muted small" },
      "qBittorrent reports paths in ITS namespace; this container sees a different " +
      "layout. Leave empty if they are identical — that is the common case when the " +
      "container mounts the client's download folder at the same path.")));
  kids.push(mapBox);
  kids.push(el("div", { class: "row" },
    el("button", { class: "ghost small", onclick: () => addMapRow(null) }, "+ mapping")));

  kids.push(result);
  kids.push(el("div", { class: "row" },
    el("button", { class: "ghost", onclick: async () => {
      result.replaceChildren(el("span", { class: "muted", text: "Testing…" }));
      try {
        const r = await api("/clients/test", { method: "POST", body: JSON.stringify(collect()) });
        result.replaceChildren(testSummary(r));
      } catch (e) {
        result.replaceChildren(el("div", { class: "reviewnote bad", text: e.message }));
      }
    }}, "Test"),
    el("button", { class: "action", onclick: async () => {
      try {
        const body = collect();
        const path = body.id ? "/clients/" + body.id : "/clients";
        await api(path, { method: body.id ? "PUT" : "POST", body: JSON.stringify(body) });
        toast("Saved.");
        refreshAll();
      } catch (e) { toast(e.message, "bad"); }
    }}, "Save"),
    isNew ? null : el("button", { class: "ghost danger", onclick: () => {
      confirmBox("Delete this client?", [
        "Orphans discovered through it are removed from the database.",
        "No files are touched — this only forgets a connection.",
      ], async () => {
        try { await api("/clients/" + c.id, { method: "DELETE" }); toast("Deleted."); refreshAll(); }
        catch (e) { toast(e.message, "bad"); }
      }, true);
    }}, "Delete")));

  return el("div", { class: "card plan" }, ...kids);
}

function testSummary(r) {
  if (r.scannable === false) {
    return el("div", { class: "reviewnote bad" },
      el("strong", { text: "Connected, but will not be scanned. " }), r.refusal || "");
  }
  const kids = [
    el("div", null, el("strong", { text: "Connected. " }),
      "qBittorrent " + (r.app_version || "?") + ", Web API " + (r.api_version || "?") +
      ", auth " + (r.auth_mode || "?")),
  ];
  if (r.total !== undefined) {
    kids.push(el("div", { class: "big" },
      String(r.uncategorised_complete || 0) + " uncategorised completed downloads"));
    kids.push(el("div", { class: "muted" },
      bytes(r.uncategorised_bytes || 0) + " across them · " +
      (r.uncategorised || 0) + " uncategorised of " + r.total + " total"));
  }
  if (r.warning) kids.push(el("div", { class: "muted", text: r.warning }));
  return el("div", { class: "reviewnote good" }, ...kids);
}

function field(label, input) {
  return el("label", null, el("span", { text: label }), input);
}

// ---------------------------------------------------------------- settings

const SETTINGS_FIELDS = [
  ["ops__dry_run", "Dry run", "select", ["true", "false"],
   "Ships on. While it is on, executing a plan is refused."],
  ["ops__auto_file", "Auto-file confident plans", "select", ["false", "true"],
   "Off means every plan waits for you, whatever its score."],
  ["ops__mode", "Placement", "select", ["copy", "link_or_copy", "link"],
   "Copy is the default. Hardlinking is used only where a real link(2) probe passed."],
  ["ops__collision", "When the destination exists", "select", ["skip", "suffix", "fail"],
   "There is no overwrite option, and there will not be one."],
  ["ops__client_write", "Mark filed items in the client", "select", ["tag", "none"],
   "A tag, never a category: setting a category relocates data under Automatic Torrent Management."],
  ["ops__max_plans_per_run", "Max plans per run", "number", null,
   "Byte-blind: 25 plans can be 25 GB or 25 TB. The free-space check is what bounds the damage."],
  ["ops__reserve_bytes", "Reserve bytes (floor)", "number", null,
   "The effective reserve is max(this, 5% of the filesystem)."],
  ["scan__interval_seconds", "Scan interval (s)", "number", null, ""],
  ["scan__settle_seconds", "Settle window (s)", "number", null,
   "A torrent that finished 30 seconds ago may still be moving bytes."],
  ["detect__auto_threshold", "Auto-file threshold", "text", null,
   "Below this, or on any ambiguity, the item goes to review."],
  ["detect__pdf_default", "Bare PDF goes to", "select", ["review", "ebook", "comic"],
   "A PDF with no other signal is genuinely undecidable between a comic and a book."],
];

async function loadSettings() {
  let r;
  try { r = await api("/settings"); } catch (_) { return; }
  const form = $("#settingsform");
  form.replaceChildren();

  SETTINGS_FIELDS.forEach(([key, label, kind, opts, help]) => {
    let input;
    if (kind === "select") {
      input = el("select", null, ...opts.map((o) => el("option", { value: o, text: o })));
    } else {
      input = el("input", { type: kind });
    }
    input.id = "set_" + key;
    const val = r.settings && r.settings[key] !== undefined ? r.settings[key] : settingDefault(key, r.defaults);
    input.value = val;

    const l = el("label", null, el("span", { text: label }), input);
    if (r.read_only && r.read_only[key]) {
      input.disabled = true;
      l.appendChild(el("span", { class: "muted small", text: "pinned by an environment variable" }));
    } else if (help) {
      l.appendChild(el("span", { class: "muted small", text: help }));
    }
    form.appendChild(l);
  });
}

function settingDefault(key, d) {
  if (!d) return "";
  const m = {
    ops__dry_run: d.DryRun, ops__auto_file: d.AutoFile, ops__mode: d.Mode,
    ops__collision: d.Collision, ops__client_write: d.ClientWrite,
    ops__max_plans_per_run: d.MaxPlansPerRun, ops__reserve_bytes: d.ReserveBytes,
    detect__auto_threshold: d.AutoThreshold, detect__pdf_default: d.PDFDefault,
    scan__interval_seconds: 900, scan__settle_seconds: 300,
  };
  const v = m[key];
  return v === undefined || v === null ? "" : String(v);
}

// -------------------------------------------------------------------- wiring

document.querySelectorAll("nav button").forEach((b) => {
  b.onclick = () => show(b.dataset.tab);
});

$("#savebtn").onclick = async () => {
  const body = {};
  document.querySelectorAll("[id^=set_]").forEach((i) => {
    if (!i.disabled) body[i.id.slice(4)] = String(i.value);
  });
  try {
    await api("/settings", { method: "PUT", body: JSON.stringify(body) });
    toast("Saved. Restart to apply the scan interval.");
    refreshAll();
  } catch (e) { toast(e.message, "bad"); }
};

$("#scanbtn").onclick = async () => {
  const b = $("#scanbtn");
  b.disabled = true;
  b.textContent = "Scanning…";
  try {
    const s = await api("/scan", { method: "POST" });
    toast(s.orphans + " orphans across " + s.items + " items (" + bytes(s.bytes) + ")");
    (s.errors || []).forEach((e) => toast(e, "bad"));
    await refreshAll();
  } catch (e) {
    toast(e.message, "bad");
  } finally {
    b.disabled = false;
    b.textContent = "Scan now";
  }
};

async function loadEvents() {
  try {
    const r = await api("/events");
    const rows = (r.events || []).map((e) =>
      el("tr", null,
        el("td", { class: "mono small", text: ago(e.TS) }),
        el("td", null, el("code", { text: e.Code })),
        el("td", { class: "small", text: e.Message || "" })));
    $("#eventtbl tbody").replaceChildren(...(rows.length ? rows : [emptyRow(3, "No events yet.")]));
  } catch (_) { /* the banner already reports API failure */ }
}

refreshAll();
loadSettings();
loadEvents();
setInterval(() => { refreshAll(); loadEvents(); }, 30000);
