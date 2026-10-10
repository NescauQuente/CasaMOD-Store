/**
 * CasaMOD: casamod-store-notifier v1.0.0
 * Installed and managed by the CasaMOD Store container.
 * Reads status.json (written by the container next to this file) and shows a
 * small notice on the CasaOS desktop when updates are waiting.
 */
(function () {
  "use strict";

  var BASE = "/mod/casamod-store-notifier/";
  var POLL_MS = 30 * 60 * 1000;
  var DISMISS_KEY = "casamod_store_notifier_dismissed";
  var ROOT_ID = "casamod-store-notifier";

  // Texts in the 31 CasaOS languages: [updates available, open store, dismiss].
  var I18N = {
    ar_sa: ["تحديثات متاحة: {n}", "فتح المتجر", "إخفاء"],
    be_by: ["Даступныя абнаўленні: {n}", "Адкрыць краму", "Схаваць"],
    bg_bg: ["Налични актуализации: {n}", "Отвори магазина", "Скрий"],
    ca_es: ["Actualitzacions disponibles: {n}", "Obre la botiga", "Amaga"],
    cs_cz: ["Dostupné aktualizace: {n}", "Otevřít obchod", "Skrýt"],
    de_de: ["Verfügbare Updates: {n}", "Store öffnen", "Ausblenden"],
    el_gr: ["Διαθέσιμες ενημερώσεις: {n}", "Άνοιγμα καταστήματος", "Απόκρυψη"],
    en_us: ["Updates available: {n}", "Open store", "Dismiss"],
    es_es: ["Actualizaciones disponibles: {n}", "Abrir tienda", "Ocultar"],
    fr_fr: ["Mises à jour disponibles : {n}", "Ouvrir la boutique", "Masquer"],
    hr_hr: ["Dostupna ažuriranja: {n}", "Otvori trgovinu", "Sakrij"],
    hu_hu: ["Elérhető frissítések: {n}", "Áruház megnyitása", "Elrejtés"],
    id_id: ["Pembaruan tersedia: {n}", "Buka toko", "Tutup"],
    it_it: ["Aggiornamenti disponibili: {n}", "Apri lo store", "Nascondi"],
    ja_jp: ["利用可能なアップデート: {n}", "ストアを開く", "閉じる"],
    ko_kr: ["사용 가능한 업데이트: {n}", "스토어 열기", "닫기"],
    nl_nl: ["Beschikbare updates: {n}", "Winkel openen", "Verbergen"],
    no_no: ["Tilgjengelige oppdateringer: {n}", "Åpne butikken", "Skjul"],
    pl_pl: ["Dostępne aktualizacje: {n}", "Otwórz sklep", "Ukryj"],
    pt_br: ["Atualizações disponíveis: {n}", "Abrir loja", "Dispensar"],
    pt_pt: ["Atualizações disponíveis: {n}", "Abrir loja", "Ignorar"],
    ro_ro: ["Actualizări disponibile: {n}", "Deschide magazinul", "Ascunde"],
    ru_ru: ["Доступные обновления: {n}", "Открыть магазин", "Скрыть"],
    sk_sk: ["Dostupné aktualizácie: {n}", "Otvoriť obchod", "Skryť"],
    sl_si: ["Razpoložljive posodobitve: {n}", "Odpri trgovino", "Skrij"],
    sv_se: ["Tillgängliga uppdateringar: {n}", "Öppna butiken", "Dölj"],
    tr_tr: ["Mevcut güncellemeler: {n}", "Mağazayı aç", "Gizle"],
    uk_ua: ["Доступні оновлення: {n}", "Відкрити магазин", "Сховати"],
    vi_vn: ["Có bản cập nhật: {n}", "Mở cửa hàng", "Ẩn"],
    zh_cn: ["可用更新：{n}", "打开商店", "忽略"],
    zh_tw: ["可用更新：{n}", "開啟商店", "忽略"]
  };

  function lang() {
    var l = "";
    try { l = (localStorage.getItem("lang") || "").toLowerCase().replace("-", "_"); } catch (e) {}
    if (!I18N[l]) {
      var nav = (navigator.language || "en_us").toLowerCase().replace("-", "_");
      l = I18N[nav] ? nav : "";
      if (!l) {
        var prefix = nav.split("_")[0];
        for (var k in I18N) { if (k.indexOf(prefix + "_") === 0) { l = k; break; } }
      }
    }
    return I18N[l] ? l : "en_us";
  }

  function loggedIn() {
    try { return !!localStorage.getItem("access_token"); } catch (e) { return true; }
  }

  function signature(status) {
    return (status.items || []).map(function (it) {
      return it.type + ":" + it.id + ":" + it.latest;
    }).sort().join("|");
  }

  function storeURL(status) {
    if (status.ui_url) return status.ui_url;
    return "http://" + location.hostname + ":" + (status.ui_port || 8418) + "/#updates";
  }

  function remove() {
    var el = document.getElementById(ROOT_ID);
    if (el) el.remove();
  }

  function style(el, css) {
    for (var k in css) el.style[k] = css[k];
  }

  function render(status) {
    var count = (status.items || []).length;
    var sig = signature(status);
    var dismissed = "";
    try { dismissed = localStorage.getItem(DISMISS_KEY) || ""; } catch (e) {}
    if (!count || !loggedIn() || dismissed === sig) {
      remove();
      return;
    }
    var t = I18N[lang()];
    remove();
    var root = document.createElement("div");
    root.id = ROOT_ID;
    root.setAttribute("role", "status");
    root.title = "CasaMOD Store\n" + (status.items || []).map(function (it) { return "• " + (it.name || it.id); }).join("\n");
    style(root, {
      position: "fixed", right: "16px", bottom: "16px", zIndex: "2000",
      display: "flex", alignItems: "center", gap: "10px",
      padding: "8px 8px 8px 14px", borderRadius: "999px",
      background: "rgba(20, 24, 31, 0.62)", color: "#fff",
      backdropFilter: "blur(16px)", webkitBackdropFilter: "blur(16px)",
      boxShadow: "0 4px 18px rgba(0,0,0,0.25)",
      font: "500 13px/1.2 -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
    });

    var dot = document.createElement("span");
    style(dot, { width: "8px", height: "8px", borderRadius: "50%", background: "#4ade80", flex: "none" });
    var text = document.createElement("span");
    text.className = "casamod-store-notifier-text";
    text.textContent = t[0].replace("{n}", count);

    var link = document.createElement("a");
    link.className = "casamod-store-notifier-open";
    link.href = storeURL(status);
    link.target = "_blank";
    link.rel = "noopener";
    link.textContent = t[1];
    style(link, {
      color: "#fff", background: "rgba(255,255,255,0.16)", padding: "5px 12px",
      borderRadius: "999px", textDecoration: "none", whiteSpace: "nowrap"
    });

    var close = document.createElement("button");
    close.className = "casamod-store-notifier-dismiss";
    close.type = "button";
    close.setAttribute("aria-label", t[2]);
    close.title = t[2];
    close.textContent = "×";
    style(close, {
      border: "0", background: "transparent", color: "rgba(255,255,255,0.75)",
      font: "400 18px/1 sans-serif", cursor: "pointer", padding: "0 6px"
    });
    close.addEventListener("click", function () {
      try { localStorage.setItem(DISMISS_KEY, sig); } catch (e) {}
      remove();
    });

    root.appendChild(dot);
    root.appendChild(text);
    root.appendChild(link);
    root.appendChild(close);
    document.body.appendChild(root);
  }

  function refresh() {
    fetch(BASE + "status.json?t=" + Date.now(), { cache: "no-store" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (status) { if (status) render(status); else remove(); })
      .catch(function () {});
  }

  function start() {
    refresh();
    setInterval(refresh, POLL_MS);
    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState === "visible") refresh();
    });
    // CasaOS is a single page app: the user may log in after the page loads.
    window.addEventListener("storage", function (e) {
      if (e.key === "access_token" || e.key === "lang") refresh();
    });
  }

  if (document.body) start();
  else document.addEventListener("DOMContentLoaded", start);
})();
