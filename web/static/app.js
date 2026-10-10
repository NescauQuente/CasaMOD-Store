/* CasaMOD Store web UI. Plain JavaScript, no build step. */
(function () {
  "use strict";

  // ---------------------------------------------------------------- i18n
  // The 31 CasaOS languages, one JSON file each in lang/. English is always
  // loaded too, as the fallback for any missing text.
  var LANGS = ["ar_sa", "be_by", "bg_bg", "ca_es", "cs_cz", "de_de", "el_gr", "en_us", "es_es", "fr_fr",
    "hr_hr", "hu_hu", "id_id", "it_it", "ja_jp", "ko_kr", "nl_nl", "no_no", "pl_pl", "pt_br", "pt_pt",
    "ro_ro", "ru_ru", "sk_sk", "sl_si", "sv_se", "tr_tr", "uk_ua", "vi_vn", "zh_cn", "zh_tw"];
  var STRINGS = { en_us: {} };
  var LANG = "en_us";

  // pickLang maps a browser language ("pt-BR", "zh-Hant-TW", "nb") to a file.
  function pickLang(tag) {
    var t = String(tag || "").toLowerCase().replace(/-/g, "_");
    var alias = { en: "en_us", pt: "pt_br", nb: "no_no", nn: "no_no", no: "no_no", zh_hant: "zh_tw", zh_hk: "zh_tw", zh_mo: "zh_tw" };
    if (LANGS.indexOf(t) >= 0) return t;
    if (alias[t]) return alias[t];
    if (/^zh_(hant|tw|hk|mo)/.test(t)) return "zh_tw";
    var base = t.split("_")[0];
    if (alias[base]) return alias[base];
    for (var i = 0; i < LANGS.length; i++) if (LANGS[i].split("_")[0] === base) return LANGS[i];
    return "";
  }

  function chooseLang() {
    try {
      var forced = pickLang(new URLSearchParams(location.search).get("lang"));
      if (forced) return forced;
    } catch (e) {}
    var list = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language];
    for (var i = 0; i < list.length; i++) {
      var l = pickLang(list[i]);
      if (l) return l;
    }
    return "en_us";
  }

  function loadLangFile(code) {
    return fetch("lang/" + code + ".json").then(function (r) { return r.ok ? r.json() : {}; }, function () { return {}; });
  }

  function loadLang() {
    LANG = chooseLang();
    var wanted = LANG === "en_us" ? ["en_us"] : ["en_us", LANG];
    return Promise.all(wanted.map(loadLangFile)).then(function (files) {
      wanted.forEach(function (code, i) { STRINGS[code] = files[i]; });
    });
  }

  function locale() { return LANG.replace("_", "-"); }

  function t(key, vars) {
    var s = (STRINGS[LANG] && STRINGS[LANG][key]) || STRINGS.en_us[key] || key;
    if (vars) Object.keys(vars).forEach(function (k) { s = s.split("{" + k + "}").join(vars[k]); });
    return s;
  }

  function applyI18n() {
    document.documentElement.lang = locale();
    document.documentElement.dir = LANG === "ar_sa" ? "rtl" : "ltr";
    document.querySelectorAll("[data-i18n]").forEach(function (el) { el.textContent = t(el.dataset.i18n); });
    document.querySelectorAll("[data-i18n-placeholder]").forEach(function (el) { el.placeholder = t(el.dataset.i18nPlaceholder); });
  }

  // ---------------------------------------------------------------- helpers
  function $(id) { return document.getElementById(id); }

  function el(tag, attrs, children) {
    var e = document.createElement(tag);
    if (attrs) Object.keys(attrs).forEach(function (k) {
      var v = attrs[k];
      if (v === null || v === undefined || v === false) return;
      if (k === "text") e.textContent = v;
      else if (k === "class") e.className = v;
      else if (k.indexOf("on") === 0) e.addEventListener(k.slice(2), v);
      else if (k === "dataset") Object.keys(v).forEach(function (d) { e.dataset[d] = v[d]; });
      else e.setAttribute(k, v === true ? "" : v);
    });
    (children || []).forEach(function (c) {
      if (c === null || c === undefined || c === false) return;
      e.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
    return e;
  }

  function api(method, path, body) {
    var opts = { method: method, headers: { "X-CasaMOD-Store": "1" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return fetch("api/" + path, opts).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (data) {
        if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
        return data;
      });
    }, function () { throw new Error(t("err.network")); });
  }

  function toast(msg, isError) {
    var box = el("div", { class: "toast" + (isError ? " error" : ""), role: isError ? "alert" : "status", text: msg });
    $("toasts").appendChild(box);
    setTimeout(function () { box.remove(); }, isError ? 9000 : 4000);
  }

  function fmtDate(s) {
    if (!s) return "";
    var d = new Date(s);
    if (isNaN(d) || d.getFullYear() < 2000) return "";
    return d.toLocaleString(locale(), { dateStyle: "short", timeStyle: "short" });
  }

  function short(sha) { return (sha || "").slice(0, 7); }

  function key(type, id) { return type + ":" + id; }

  function sourceURL(src) {
    var u = "https://github.com/" + src.repo;
    if (src.branch || src.path) u += "/tree/" + encodeURIComponent(src.branch || "HEAD") + (src.path ? "/" + src.path : "");
    return u;
  }

  function sourceText(src) {
    return src.repo + (src.branch ? "@" + src.branch : "") + (src.path ? " / " + src.path : "");
  }

  // ---------------------------------------------------------------- state
  var state = null;      // /api/state
  var catalog = null;    // /api/store
  var filters = { q: "", type: "", source: "" };
  var selected = {};     // store selection, by item index key
  var inFlight = 0;
  var pollTimer = null;

  function installedMap() {
    var m = {};
    (state ? state.installed : []).forEach(function (it) { m[it.key] = it; });
    return m;
  }

  function manualMap() {
    var m = {};
    (state ? state.manual : []).forEach(function (it) { m[key(it.type, it.id)] = it; });
    return m;
  }

  function loadState() {
    return api("GET", "state").then(function (s) {
      state = s;
      renderAll();
      schedulePoll();
    }).catch(function (e) {
      schedulePoll();
      throw e;
    });
  }

  function schedulePoll() {
    clearTimeout(pollTimer);
    var fast = inFlight > 0 || (state && (state.busy || state.restart.pending));
    pollTimer = setTimeout(function () { loadState().catch(function () {}); }, fast ? 1500 : 30000);
  }

  function loadStore(refresh) {
    $("btn-refresh-store").disabled = true;
    return api("GET", "store" + (refresh ? "?refresh=1" : "")).then(function (c) {
      catalog = c;
      renderSourceFilter();
      renderStore();
    }).catch(function (e) { toast(e.message, true); })
      .then(function () { $("btn-refresh-store").disabled = false; });
  }

  // Runs an operation with the busy indicator and refreshes the state.
  function run(promise) {
    inFlight++;
    renderBusy();
    schedulePoll();
    return promise.then(function (res) {
      inFlight--;
      return loadState().then(function () { return res; }, function () { return res; });
    }, function (err) {
      inFlight--;
      loadState().catch(function () {});
      throw err;
    });
  }

  // ---------------------------------------------------------------- render
  function renderAll() {
    $("version").textContent = /^v?\d/.test(state.version || "") ? "v" + state.version.replace(/^v/, "") : (state.version || "");
    var updates = state.installed.filter(function (i) { return i.has_update; }).length;
    $("count-installed").textContent = state.installed.length || "";
    if (selfUpdate()) updates++;
    $("count-updates").textContent = updates || "";
    renderBusy();
    renderBanners();
    renderInstalled();
    renderUpdates();
    renderSettings();
    if (catalog) renderStore();
  }

  function renderBusy() {
    var text = (state && state.busy) || "";
    $("busy").hidden = !(inFlight > 0 || text);
    $("busy-text").textContent = text || "…";
  }

  function selfUpdate() { return state.self && state.self.has_update ? state.self : null; }

  function renderBanners() {
    var box = $("banners");
    box.textContent = "";
    var r = state.restart || {};
    if (r.pending) {
      box.appendChild(el("div", { class: "banner info", id: "banner-pending" }, [el("span", { text: t("banner.pending") })]));
    } else if (r.needed && state.docker_available) {
      box.appendChild(el("div", { class: "banner err", id: "banner-restart" }, [
        el("span", { text: t("banner.needed", { error: r.last_error || "" }) }),
        el("button", { class: "btn", text: t("banner.restart"), onclick: restartNow })
      ]));
    }
    if (!state.docker_available) {
      box.appendChild(el("div", { class: "banner warn", id: "banner-docker" }, [el("span", { text: t("banner.noDocker") })]));
    }
  }

  function renderSourceFilter() {
    var sel = $("source-filter");
    var cur = sel.value;
    while (sel.options.length > 1) sel.remove(1);
    (catalog.sources || []).forEach(function (s) {
      sel.appendChild(el("option", { value: s.label, text: s.label + " (" + s.count + ")" }));
    });
    sel.value = cur;
    var errs = $("source-errors");
    errs.textContent = "";
    (catalog.sources || []).forEach(function (s) {
      if (s.error) errs.appendChild(el("div", { class: "banner err source-error" }, [
        el("span", { text: t("store.sourceError", { source: s.label, error: s.error }) })
      ]));
      (s.warnings || []).forEach(function (w) {
        var inner = (w.inner || []).slice(0, 3).join(", ") + ((w.inner || []).length > 3 ? ", …" : "");
        errs.appendChild(el("div", { class: "banner warn source-warning", dataset: { kind: w.kind } }, [
          el("span", { text: t("warn." + w.kind, {
            source: s.label, path: w.path || "/", type: w.type ? t("type." + w.type).toLowerCase() : "", inner: inner
          }) })
        ]));
      });
    });
  }

  function itemKey(item) { return key(item.type, item.id) + "|" + item.source.repo + "|" + item.source.path; }

  function filteredItems() {
    var q = filters.q.toLowerCase();
    return (catalog.items || []).filter(function (it) {
      if (filters.type && it.type !== filters.type) return false;
      if (filters.source && it.origin_label !== filters.source) return false;
      if (q && (it.name + " " + it.id + " " + (it.description || "") + " " + (it.author || "")).toLowerCase().indexOf(q) < 0) return false;
      return true;
    });
  }

  function renderStore() {
    var grid = $("store-grid");
    grid.textContent = "";
    var inst = installedMap();
    var manual = manualMap();
    var items = filteredItems();
    $("store-empty").hidden = items.length > 0;
    items.forEach(function (item) {
      var k = key(item.type, item.id);
      var mine = inst[k];
      var sameSource = mine && mine.source.repo === item.source.repo && mine.source.path === item.source.path;
      var ik = itemKey(item);
      var badges = [
        el("span", { class: "badge", text: t("type." + item.type) }),
        el("span", { class: "badge", text: item.origin_label })
      ];
      if (item.version) badges.push(el("span", { class: "badge", text: "v" + item.version }));
      var action;
      if (sameSource && mine.has_update) {
        badges.push(el("span", { class: "badge accent", text: t("state.update") }));
        action = el("button", { class: "btn primary small", text: t("common.update"), onclick: function () { doUpdate([mine.key], mine.name); } });
      } else if (sameSource) {
        badges.push(el("span", { class: "badge ok", text: "✓ " + t("state.installed") }));
        action = null;
      } else {
        if (manual[k]) badges.push(el("span", { class: "badge warn", text: t("state.manual") }));
        action = el("button", { class: "btn primary small", text: t("common.install"), onclick: function () { installChecked([toRequest(item)]); } });
      }
      var preview = cardPicture(item);
      var checkbox = sameSource ? null : el("input", {
        type: "checkbox", class: "select", "aria-label": item.name, checked: !!selected[ik],
        onchange: function (e) {
          if (e.target.checked) selected[ik] = item; else delete selected[ik];
          card.classList.toggle("selected", e.target.checked);
          renderSelection();
        }
      });
      var card = el("article", { class: "card" + (selected[ik] ? " selected" : ""), dataset: { key: k, origin: item.origin } }, [
        checkbox,
        preview,
        el("div", { class: "body" }, [
          el("div", { class: "title" }, [el("strong", { text: item.name })]),
          el("div", { class: "meta" }, badges),
          el("div", { class: "desc", text: item.description || "" })
        ]),
        el("div", { class: "actions" }, [
          action,
          el("a", { href: sourceURL(item.source), target: "_blank", rel: "noopener", class: "hint", text: item.source.repo.split("/")[0] + " ↗" })
        ])
      ]);
      grid.appendChild(card);
    });
    renderSelection();
  }

  // The card picture: the item's preview, else its owner's GitHub picture,
  // else an icon. Each step falls back to the next when it fails to load.
  function cardPicture(item) {
    var box = el("div", { class: "preview" });
    var icon = item.type === "wallpaper" ? "🖼" : "🧩";
    var steps = [];
    if (item.preview) steps.push({ src: item.preview, cls: "" });
    if (item.avatar) steps.push({ src: item.avatar, cls: "avatar" });
    (function next() {
      box.textContent = "";
      var step = steps.shift();
      if (!step) { box.textContent = icon; return; }
      box.appendChild(el("img", { src: step.src, alt: "", loading: "lazy", class: step.cls || null, onerror: next }));
    })();
    return box;
  }

  function renderSelection() {
    var n = Object.keys(selected).length;
    $("selection-bar").hidden = n === 0;
    $("selection-text").textContent = t("store.selected", { n: n });
  }

  function toRequest(item) {
    return {
      id: item.id, type: item.type, name: item.name, description: item.description || "",
      source: item.source, origin: item.origin
    };
  }

  function statusBadge(it) {
    if (it.missing) return el("span", { class: "badge err", text: t("state.missing") });
    if (it.has_update) return el("span", { class: "badge accent", text: t("state.update") });
    if (it.last_error) return el("span", { class: "badge warn", text: t("state.error") });
    return el("span", { class: "badge ok", text: t("state.upToDate") });
  }

  function policySelect(it) {
    var sel = el("select", { "aria-label": t("url.policy"), class: "policy" }, [
      el("option", { value: "notify", text: t("policy.notify") }),
      el("option", { value: "auto", text: t("policy.auto") })
    ]);
    sel.value = it.policy;
    sel.addEventListener("change", function () {
      api("POST", "policy", { key: it.key, policy: sel.value })
        .then(function () { return loadState(); })
        .catch(function (e) { toast(e.message, true); });
    });
    return sel;
  }

  function installedRow(it, forUpdates) {
    var sub = [
      el("a", { href: sourceURL(it.source), target: "_blank", rel: "noopener", text: sourceText(it.source) }),
      " · ",
      short(it.sha)
    ];
    if (it.has_update) {
      sub.push(" → " + short(it.latest_sha));
    }
    var controls = [policySelect(it)];
    if (it.has_update || it.missing) {
      controls.push(el("button", { class: "btn primary small", text: it.missing ? t("common.reinstall") : t("common.update"),
        onclick: function () { doUpdate([it.key], it.name); } }));
    }
    if (!forUpdates) {
      controls.push(el("button", { class: "btn danger small", text: t("common.remove"), onclick: function () { doRemove(it); } }));
    }
    return el("div", { class: "row-item", dataset: { key: it.key } }, [
      el("div", { class: "info" }, [
        el("div", { class: "name" }, [it.name, el("span", { class: "badge", text: t("type." + it.type) }), statusBadge(it)]),
        el("div", { class: "sub" }, sub),
        it.last_error ? el("div", { class: "error-line", text: it.last_error }) : null
      ]),
      el("div", { class: "controls" }, controls)
    ]);
  }

  function renderInstalled() {
    var list = $("installed-list");
    list.textContent = "";
    state.installed.forEach(function (it) { list.appendChild(installedRow(it, false)); });
    $("installed-empty").hidden = state.installed.length > 0;
    var ml = $("manual-list");
    ml.textContent = "";
    state.manual.forEach(function (m) {
      ml.appendChild(el("div", { class: "row-item", dataset: { key: key(m.type, m.id) } }, [
        el("div", { class: "info" }, [el("div", { class: "name" }, [m.id, el("span", { class: "badge", text: t("type." + m.type) })])]),
        el("div")
      ]));
    });
    $("manual-empty").hidden = state.manual.length > 0;
  }

  function renderUpdates() {
    var list = $("updates-list");
    list.textContent = "";
    var ups = state.installed.filter(function (i) { return i.has_update || i.missing; });
    var self = selfUpdate();
    if (self) {
      list.appendChild(el("div", { class: "row-item", dataset: { key: "store:casamod-store" } }, [
        el("div", { class: "info" }, [
          el("div", { class: "name" }, ["CasaMOD Store", el("span", { class: "badge accent", text: t("state.update") })]),
          el("div", { class: "sub", text: short(self.commit) + " → " + short(self.latest_sha) })
        ]),
        el("div", { class: "controls" }, [policySelect(self)])
      ]));
    }
    ups.forEach(function (it) { list.appendChild(installedRow(it, true)); });
    $("updates-empty").hidden = ups.length > 0 || !!self;
    $("btn-update-all").disabled = !state.installed.some(function (i) { return i.has_update; });
    var info = state.last_check ? t("updates.last", { when: fmtDate(state.last_check) }) : t("updates.never");
    if (state.config.check_interval_hours > 0) {
      if (state.next_check) info += " · " + t("updates.next", { when: fmtDate(state.next_check) });
    } else {
      info += " · " + t("updates.off");
    }
    $("check-info").textContent = info;
    var log = $("log");
    log.textContent = "";
    state.log.slice().reverse().forEach(function (l) {
      log.appendChild(el("li", { class: l.level }, [el("time", { text: fmtDate(l.time) }), l.message]));
    });
  }

  var settingsDirty = false;
  function renderSettings() {
    if (settingsDirty) return;
    var c = state.config;
    $("set-token").placeholder = c.has_token ? t("settings.tokenSaved") : "ghp_…";
    $("btn-clear-token").disabled = !c.has_token;
    $("set-catalogs").value = (c.catalogs || []).join("\n");
    $("set-casamod-mods").checked = c.include_casamod_mods;
    $("set-interval").value = c.check_interval_hours;
    if (state.self) $("set-self-policy").value = state.self.policy;
    $("set-notifier").checked = c.notifier;
    $("set-public-url").value = c.public_url || "";
    $("set-port").value = c.ui_port;
    $("set-container").value = c.casamod_container;
  }

  // ---------------------------------------------------------------- actions
  function reportResults(res, doneKey, names) {
    var conflicts = [];
    (res.results || []).forEach(function (r, i) {
      if (r.ok) toast(t(doneKey, { name: names[i] || r.key }));
      else if (r.conflict) conflicts.push(i);
      else toast((names[i] || r.key) + ": " + r.error, true);
    });
    return conflicts;
  }

  // Wallpapers are played by the Live Wallpapers mod: when it is missing,
  // offer to install it in the same batch (one CasaMOD restart).
  var LIVE_WALLPAPERS = {
    id: "live-wallpapers", type: "mod", name: "Live Wallpapers", description: "", origin: "catalog",
    source: { repo: "UsuarioDeJava/CasaMODs", branch: "", path: "casaos-live-wallpapers/live-wallpapers" }
  };

  function hasLiveWallpapers() {
    var re = /^live-?wallpapers?$/i;
    return state.installed.some(function (i) { return i.type === "mod" && re.test(i.id) && !i.missing; }) ||
      state.manual.some(function (m) { return m.type === "mod" && re.test(m.id); });
  }

  function liveWallpapersRequest() {
    var item = catalog && (catalog.items || []).filter(function (i) {
      return i.type === "mod" && i.id === LIVE_WALLPAPERS.id;
    })[0];
    return item ? toRequest(item) : LIVE_WALLPAPERS;
  }

  function installChecked(reqs) {
    var wantsWallpaper = reqs.some(function (r) { return r.type === "wallpaper"; });
    var hasLW = reqs.some(function (r) { return r.type === "mod" && r.id === LIVE_WALLPAPERS.id; });
    if (!wantsWallpaper || hasLW || hasLiveWallpapers()) return doInstall(reqs);
    var dlg = $("dlg-lw");
    $("btn-lw-ok").onclick = function () { dlg.close(); doInstall(reqs); };
    $("btn-lw-install").onclick = function () { dlg.close(); doInstall([liveWallpapersRequest()].concat(reqs)); };
    dlg.showModal();
  }

  function doInstall(reqs) {
    var names = reqs.map(function (r) { return r.name || r.id; });
    return run(api("POST", "install", { items: reqs })).then(function (res) {
      var conflicts = reportResults(res, "done.installed", names);
      var forced = conflicts.filter(function (i) {
        return window.confirm(res.results[i].error);
      }).map(function (i) { var r = Object.assign({}, reqs[i]); r.force = true; return r; });
      if (forced.length) return doInstall(forced);
      return res;
    }).catch(function (e) { toast(e.message, true); });
  }

  function doUpdate(keys, name) {
    return run(api("POST", "update", { keys: keys })).then(function (res) {
      if (!res.results || !res.results.length) toast(t("done.nothing"));
      reportResults(res, "done.updated", keys.length === 1 ? [name] : []);
    }).catch(function (e) { toast(e.message, true); });
  }

  function doRemove(it) {
    if (!window.confirm(t("confirm.remove", { name: it.name }))) return;
    return run(api("POST", "remove", { keys: [it.key] })).then(function (res) {
      reportResults(res, "done.removed", [it.name]);
    }).catch(function (e) { toast(e.message, true); });
  }

  function restartNow() {
    return run(api("POST", "restart")).then(function () { toast(t("banner.restarted")); })
      .catch(function (e) { toast(e.message, true); });
  }

  // ---------------------------------------------------------------- tabs
  var TABS = ["store", "installed", "updates", "settings"];
  function showTab() {
    var name = (location.hash || "#store").slice(1);
    if (TABS.indexOf(name) < 0) name = "store";
    TABS.forEach(function (n) { $("tab-" + n).hidden = n !== name; });
    document.querySelectorAll("#tabs a").forEach(function (a) { a.classList.toggle("active", a.dataset.tab === name); });
    if (name === "store" && !catalog) loadStore(false);
  }

  // ---------------------------------------------------------------- URL dialog
  function openURLDialog() {
    $("url-form").reset();
    $("url-details").hidden = true;
    $("url-error").hidden = true;
    $("btn-url-install").disabled = true;
    $("dlg-url").showModal();
    $("url-input").focus();
  }

  function resolveURL() {
    $("url-error").hidden = true;
    api("POST", "resolve", { url: $("url-input").value }).then(function (r) {
      $("url-repo").value = r.source.repo;
      $("url-branch").value = r.source.branch || "";
      $("url-path").value = r.source.path || "";
      $("url-id").value = r.id;
      $("url-type").value = r.type;
      $("url-details").hidden = false;
      $("btn-url-install").disabled = false;
    }).catch(function (e) {
      $("url-error").textContent = e.message;
      $("url-error").hidden = false;
    });
  }

  function installFromURL() {
    var req = {
      id: $("url-id").value.trim(), type: $("url-type").value, name: $("url-id").value.trim(),
      source: { repo: $("url-repo").value.trim(), branch: $("url-branch").value.trim(), path: $("url-path").value.trim() },
      origin: "url", policy: $("url-policy").value
    };
    $("dlg-url").close();
    installChecked([req]);
  }

  // ---------------------------------------------------------------- wiring
  function wire() {
    window.addEventListener("hashchange", showTab);
    $("search").addEventListener("input", function (e) { filters.q = e.target.value; renderStore(); });
    $("type-filter").addEventListener("click", function (e) {
      var b = e.target.closest("button");
      if (!b) return;
      filters.type = b.dataset.type;
      document.querySelectorAll("#type-filter .chip").forEach(function (c) { c.classList.toggle("active", c === b); });
      renderStore();
    });
    $("source-filter").addEventListener("change", function (e) { filters.source = e.target.value; renderStore(); });
    $("btn-refresh-store").addEventListener("click", function () { loadStore(true); });
    $("btn-add-url").addEventListener("click", openURLDialog);
    $("btn-resolve").addEventListener("click", resolveURL);
    $("url-input").addEventListener("keydown", function (e) { if (e.key === "Enter") { e.preventDefault(); resolveURL(); } });
    $("btn-url-cancel").addEventListener("click", function () { $("dlg-url").close(); });
    $("btn-url-install").addEventListener("click", installFromURL);
    $("btn-clear-selection").addEventListener("click", function () { selected = {}; renderStore(); });
    $("btn-install-selected").addEventListener("click", function () {
      var reqs = Object.keys(selected).map(function (k) { return toRequest(selected[k]); });
      selected = {};
      renderSelection();
      installChecked(reqs);
    });
    $("btn-check").addEventListener("click", function () {
      run(api("POST", "check")).then(function () { toast(t("done.checked")); }).catch(function (e) { toast(e.message, true); });
    });
    $("btn-update-all").addEventListener("click", function () { doUpdate([], ""); });
    $("btn-restart").addEventListener("click", restartNow);
    $("set-self-policy").addEventListener("change", function (e) {
      e.stopPropagation();
      api("POST", "policy", { key: "store:casamod-store", policy: e.target.value })
        .then(function () { toast(t("settings.saved")); return loadState(); })
        .catch(function (err) { toast(err.message, true); });
    });
    $("settings-form").addEventListener("input", function (e) { if (e.target.id !== "set-self-policy") settingsDirty = true; });
    $("btn-clear-token").addEventListener("click", function () {
      api("POST", "settings", { clear_token: true }).then(function () {
        toast(t("settings.saved"));
        settingsDirty = false;
        return loadState();
      }).catch(function (e) { toast(e.message, true); });
    });
    $("settings-form").addEventListener("submit", function (e) {
      e.preventDefault();
      var body = {
        catalogs: $("set-catalogs").value.split("\n").map(function (s) { return s.trim(); }).filter(Boolean),
        include_casamod_mods: $("set-casamod-mods").checked,
        check_interval_hours: parseFloat($("set-interval").value) || 0,
        notifier: $("set-notifier").checked,
        public_url: $("set-public-url").value.trim(),
        ui_port: parseInt($("set-port").value, 10) || 8418,
        casamod_container: $("set-container").value.trim()
      };
      var token = $("set-token").value.trim();
      if (token) body.github_token = token;
      api("POST", "settings", body).then(function () {
        toast(t("settings.saved"));
        $("set-token").value = "";
        settingsDirty = false;
        catalog = null;
        return loadState();
      }).catch(function (e) { toast(e.message, true); });
    });
  }

  loadLang().then(function () {
    applyI18n();
    wire();
    loadState().then(showTab, function (e) { toast(e.message, true); showTab(); });
  });
})();
