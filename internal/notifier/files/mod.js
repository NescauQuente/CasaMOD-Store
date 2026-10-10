/**
 * CasaMOD: casamod-store-notifier (loader)
 * Installed and managed by the CasaMOD Store container.
 * CasaOS serves mod files without cache rules, so browsers may keep an old
 * copy of a mod for a long time. This loader stays small and stable and
 * loads notifier.js with the version from status.json, so a new notifier
 * reaches every browser as soon as the store installs it.
 */
(function () {
  "use strict";
  var BASE = "/mod/casamod-store-notifier/";
  function load(version) {
    var s = document.createElement("script");
    s.src = BASE + "notifier.js?v=" + encodeURIComponent(version);
    (document.head || document.documentElement).appendChild(s);
  }
  fetch(BASE + "status.json?t=" + Date.now(), { cache: "no-store" })
    .then(function (r) { return r.ok ? r.json() : {}; })
    .then(function (st) { load(st.script || Date.now()); }, function () { load(Date.now()); });
})();
