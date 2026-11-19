// Shell behaviour for every admin page. No framework, no build step. Every
// page works without it; controls that need it carry the class js-only.
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var VT = (window.VT = window.VT || {});
  var store = {
    get: function (k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
    set: function (k, v) { try { localStorage.setItem(k, v); } catch (e) {} },
  };
  VT.getJSON = function (url) {
    return fetch(url, { headers: { Accept: "application/json" } }).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    });
  };

  /* Menu rail: fold and open, remembered. */
  var shell = $(".shell");
  $$("[data-rail-toggle]").forEach(function (b) {
    b.addEventListener("click", function () {
      var next = shell.dataset.rail === "folded" ? "open" : "folded";
      shell.dataset.rail = next;
      store.set("rail", next);
      window.dispatchEvent(new Event("resize"));
    });
  });

  /* Light or dark: the system setting until someone picks one here. */
  $$("[data-theme-toggle]").forEach(function (b) {
    b.addEventListener("click", function () {
      var root = document.documentElement;
      var dark = root.dataset.theme ? root.dataset.theme === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
      root.dataset.theme = dark ? "light" : "dark";
      store.set("theme", root.dataset.theme);
    });
  });

  /* Numbers count to their value once, and from their old value when they change. */
  var fmt = new Intl.NumberFormat("en");
  VT.setCount = function (el, to) {
    var from = Number(el.dataset.shown || 0);
    el.dataset.count = to;
    if (reduce || from === to) { el.textContent = fmt.format(to); el.dataset.shown = to; return; }
    var t0 = performance.now();
    cancelAnimationFrame(el._raf);
    (function step(now) {
      var p = Math.min(1, (now - t0) / 600), e = 1 - Math.pow(1 - p, 3);
      el.textContent = fmt.format(Math.round(from + (to - from) * e));
      if (p < 1) el._raf = requestAnimationFrame(step); else el.dataset.shown = to;
    })(t0);
  };
  $$(".count-up[data-count]").forEach(function (el) { VT.setCount(el, Number(el.dataset.count)); });

  /* A chart's value under the pointer. The same numbers are in the page as text. */
  var tip = $("#tip");
  if (tip) {
    document.addEventListener("pointermove", function (ev) {
      var hit = ev.target.closest(".chart .hit[data-tip]");
      if (!hit) { tip.classList.toggle("show", false); return; }
      var parts = hit.dataset.tip.split("|"), b = document.createElement("b");
      b.textContent = parts[0];
      tip.textContent = "";
      tip.appendChild(b);
      tip.appendChild(document.createTextNode(parts[1] || ""));
      tip.style.left = Math.min(window.innerWidth - tip.offsetWidth - 12, ev.clientX + 14) + window.scrollX + "px";
      tip.style.top = ev.clientY - 12 - tip.offsetHeight + window.scrollY + "px";
      tip.classList.add("show");
    });
  }

  /* The pulse: how many vehicles are reporting, and how old that answer is. The page arrives with the
     server's counts; after that the live answer is asked for every 10 s and the alerts on every third. */
  var pulse = $("#pulse"), age = $("#pulse-age"), heardAt = Date.now(), quiet = {}, polls = 0;
  function span(s) { return s < 2 ? "just now" : s < 90 ? s + " s" : Math.round(s / 60) + " min"; }
  /* The counts are a live region: written only when they change, so nothing is read out again. */
  function say(el, n) { if (el.textContent !== String(n)) el.textContent = n; }
  VT.heard = function (live) {
    heardAt = Date.now();
    var reporting = live.vehicles.filter(function (v) { return !quiet[v.vehicle_id]; }).length;
    pulse.dataset.state = reporting ? "live" : "idle";
    delete pulse.dataset.known;
    say($("#pulse-reporting"), reporting);
    if (!reduce) { pulse.classList.remove("beat"); void pulse.offsetWidth; pulse.classList.add("beat"); }
  };
  /* unknown: the page has had no live answer at all, so the counts it was drawn with are not shown as current. */
  VT.lost = function (unknown) { pulse.dataset.state = "stale"; if (unknown) pulse.dataset.known = "0"; };
  VT.alerts = function (list) {
    quiet = {};
    list.alerts.forEach(function (a) { if (a.kind === "quiet") quiet[a.trip.vehicle_id] = true; });
    var n = Object.keys(quiet).length;
    say($("#pulse-quiet"), n);
    $("#pulse-q").hidden = !n;
    $$("[data-alert-count]").forEach(function (c) { c.textContent = list.count || ""; c.hidden = !list.count; });
    $$("[data-alert-link]").forEach(function (a) { a.setAttribute("aria-label", list.count ? "Alerts, " + list.count + " open" : "Alerts"); });
  };
  /* The live map asks for the same answers itself and hands them over, so the shell does not ask twice. */
  function poll() {
    if (document.hidden || VT.ownsPulse) return;
    var alerts = polls++ % 3 ? Promise.resolve() : VT.getJSON("/api/v1/admin/alerts").then(VT.alerts).catch(function () {});
    alerts.then(function () { return VT.getJSON("/api/v1/admin/vehicles/live"); }).then(VT.heard, function () { VT.lost(false); });
  }
  if (pulse) {
    setInterval(function () {
      var s = Math.round((Date.now() - heardAt) / 1000);
      if (s > 30 && !document.hidden) pulse.dataset.state = "stale";
      var stale = pulse.dataset.state === "stale";
      age.textContent = stale ? "no answer for " + (s < 90 ? s + " s" : span(s)) : s < 2 ? "updated just now" : "updated " + span(s) + " ago";
    }, 1000);
    setInterval(poll, 10000);
    document.addEventListener("visibilitychange", poll);
  }

  /* A message that confirms something leaves by itself. One that says something failed stays until closed. */
  $$("[data-toast]").forEach(function (t) {
    var close = function () { t.remove(); };
    $("[data-toast-close]", t).addEventListener("click", close);
    if (!t.hasAttribute("data-toast-stay")) setTimeout(close, 6000);
  });

  /* Chips that show the alerts of one kind. The Alerts page reloads itself every 30 s, so the chip picked
     is kept for the tab. A kind with no open alert says so, and one kept from before the reload goes back
     to All. */
  var chips = $$("[data-filter]"), rows = $$("[data-kind]");
  function hasKind(kind) { return kind === "all" || rows.some(function (row) { return row.dataset.kind === kind; }); }
  function showKind(kind) {
    chips.forEach(function (c) { c.setAttribute("aria-pressed", String(c.dataset.filter === kind)); });
    rows.forEach(function (row) { row.hidden = kind !== "all" && row.dataset.kind !== kind; });
    var none = $("#alert-none");
    if (none) none.hidden = hasKind(kind);
  }
  chips.forEach(function (chip) {
    chip.addEventListener("click", function () {
      showKind(chip.dataset.filter);
      try { sessionStorage.setItem("alert-kind", chip.dataset.filter); } catch (e) {}
    });
  });
  if (chips.length) {
    var kept = null;
    try { kept = sessionStorage.getItem("alert-kind"); } catch (e) {}
    showKind(kept && hasKind(kept) ? kept : "all");
  }

  /* A real dialog in place of confirm(). Without JS the form posts as it is. */
  var dlg;
  document.addEventListener("submit", function (ev) {
    var f = ev.target;
    if (!f.dataset.confirm) return;
    ev.preventDefault();
    if (!dlg) {
      dlg = document.createElement("dialog");
      dlg.className = "dialog";
      dlg.setAttribute("aria-labelledby", "confirm-title");
      dlg.innerHTML = '<div class="dialog-body"><h2 class="dialog-title" id="confirm-title"></h2><p class="muted" style="margin:0"></p></div><div class="dialog-actions"><button class="btn" type="button" data-no>Cancel</button><button class="btn btn-danger-fill" type="button" data-yes></button></div>';
      document.body.appendChild(dlg);
      $("[data-no]", dlg).addEventListener("click", function () { dlg.close(); });
    }
    $(".dialog-title", dlg).textContent = f.dataset.confirm;
    $("p", dlg).textContent = f.dataset.confirmText || "";
    var yes = $("[data-yes]", dlg);
    yes.textContent = f.dataset.confirmOk || "OK";
    yes.onclick = function () { dlg.close(); f.submit(); };
    dlg.showModal();
  });

  /* Copy buttons take the text of the element they name, so a key or a password is on the page once. */
  document.addEventListener("click", function (ev) {
    var b = ev.target.closest("[data-copy-from]");
    if (!b || !navigator.clipboard) return;
    navigator.clipboard.writeText(document.getElementById(b.dataset.copyFrom).textContent).then(function () {
      var old = b.innerHTML;
      b.setAttribute("aria-live", "polite");
      b.textContent = "Copied";
      setTimeout(function () { b.innerHTML = old; }, 1500);
    }, function () {});
  });

  /* A file dropped on the import box goes into its file field. */
  $$(".drop").forEach(function (d) {
    ["dragenter", "dragover"].forEach(function (n) {
      d.addEventListener(n, function (ev) { ev.preventDefault(); d.classList.add("over"); });
    });
    d.addEventListener("dragleave", function () { d.classList.toggle("over", false); });
    d.addEventListener("drop", function (ev) {
      ev.preventDefault();
      d.classList.toggle("over", false);
      if (ev.dataTransfer.files.length) $("input", d).files = ev.dataTransfer.files;
    });
  });

  /* Search: jump to a page, route, vehicle, driver or trip. "/" or Ctrl K opens it. Pages come from the
     menu, routes from the schedule (asked for once), the rest from the list endpoints as the term is typed. */
  var pal = $("#palette"), q = $("#palette-q"), list = $("#palette-list"), shown = [], found = [], sel = 0, routes = null, asked = 0, timer = 0;
  var pages = $$(".rail a.rail-item").map(function (a) {
    return { g: "Pages", i: $("use", a).getAttribute("href").split("#")[1], t: $(".rail-label", a).textContent, h: a.getAttribute("href") };
  });
  function has(it, term) { return (it.t + " " + (it.s || "")).toLowerCase().indexOf(term) !== -1; }
  function draw() {
    var term = q.value.trim().toLowerCase();
    shown = pages.concat(routes || []).filter(function (it) { return !term || has(it, term); }).concat(term ? found : []);
    sel = Math.min(sel, Math.max(0, shown.length - 1));
    list.textContent = "";
    var group = "";
    shown.forEach(function (it, i) {
      if (it.g !== group) {
        group = it.g;
        var h = document.createElement("div"); h.className = "palette-group"; h.setAttribute("role", "presentation"); h.textContent = group; list.appendChild(h);
      }
      var a = document.createElement("a"), text = document.createElement("span"), sub = document.createElement("span");
      a.className = "palette-item"; a.href = it.h; a.id = "palette-" + i;
      a.setAttribute("role", "option"); a.setAttribute("aria-selected", String(i === sel));
      a.innerHTML = '<svg class="icon" aria-hidden="true"><use></use></svg>';
      $("use", a).setAttribute("href", "/static/icons.svg#" + it.i);
      text.style.cssText = "min-width:0;flex:1"; text.textContent = it.t;
      sub.className = "sub"; sub.style.display = "block"; sub.textContent = it.s || "";
      text.appendChild(sub); a.appendChild(text); list.appendChild(a);
    });
    if (!shown.length) {
      var e = document.createElement("div"); e.className = "empty";
      e.textContent = "Nothing matches. Try a vehicle label, a driver's name or a trip id."; list.appendChild(e);
    }
    q.setAttribute("aria-activedescendant", shown.length ? "palette-" + sel : "");
  }
  function ask() {
    var term = q.value.trim(), mine = ++asked;
    if (!term) return;
    var p = "?limit=5&q=" + encodeURIComponent(term);
    Promise.all(["vehicles", "users", "trips"].map(function (k) {
      return VT.getJSON("/api/v1/admin/" + k + p).then(function (d) { return d[k]; }, function () { return []; });
    })).then(function (r) {
      if (mine !== asked) return;
      found = r[0].map(function (v) {
        return { g: "Vehicles", i: "directions_bus", t: v.label || v.id, s: v.id, h: "/admin/vehicles/" + encodeURIComponent(v.id) + "/edit" };
      }).concat(r[1].map(function (u) {
        return { g: "Drivers and users", i: "person", t: u.name, s: u.email, h: "/admin/users/" + u.id + "/edit" };
      }), r[2].map(function (t) {
        return { g: "Trips", i: "timeline", t: "Trip " + t.id + ", " + t.gtfs_trip_id, s: t.vehicle_label + ", " + (t.status === "active" ? "running" : "ended"), h: "/admin/trips/" + t.id };
      }));
      draw();
    });
  }
  function openPalette() {
    if (pal.open) return;
    if (!routes) {
      routes = [];
      VT.getJSON("/api/v1/gtfs/routes").then(function (d) {
        routes = d.routes.map(function (r) {
          return { g: "Routes", i: "route", t: (r.short_name + " " + r.long_name).trim() || r.id, s: "", h: "/admin/reports?route_id=" + encodeURIComponent(r.id) };
        });
        draw();
      }, function () {});
    }
    sel = 0; q.value = ""; found = []; draw(); pal.showModal(); q.focus();
  }
  if (pal) {
    $$("[data-palette-open]").forEach(function (b) { b.addEventListener("click", openPalette); });
    document.addEventListener("keydown", function (ev) {
      var typing = /INPUT|TEXTAREA|SELECT/.test(ev.target.tagName || "") || ev.target.isContentEditable;
      if ((ev.key === "/" && !typing) || ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "k")) { ev.preventDefault(); openPalette(); }
    });
    q.addEventListener("input", function () { sel = 0; draw(); clearTimeout(timer); timer = setTimeout(ask, 150); });
    q.addEventListener("keydown", function (ev) {
      if ((ev.key === "ArrowDown" || ev.key === "ArrowUp") && shown.length) {
        ev.preventDefault();
        sel = Math.max(0, Math.min(shown.length - 1, sel + (ev.key === "ArrowDown" ? 1 : -1)));
        $$(".palette-item", list).forEach(function (a, i) { a.setAttribute("aria-selected", String(i === sel)); });
        q.setAttribute("aria-activedescendant", "palette-" + sel);
        $("#palette-" + sel).scrollIntoView({ block: "nearest" });
      }
      if (ev.key === "Enter" && shown[sel]) location.href = shown[sel].h;
    });
    pal.addEventListener("click", function (ev) { if (ev.target === pal) pal.close(); });
  }
})();
