// Live map. Asks the live endpoint every 10 seconds and glides each vehicle to its new place.
(function () {
  "use strict";
  var el = document.getElementById("main-map");
  var VT = window.VT || {};
  if (!el || typeof L === "undefined" || !VT.getJSON) return;
  var root = document.getElementById("map-root");
  var QUIET = Number(el.dataset.quietAfter), POLL = 10000, GTFS = el.dataset.gtfsUrl;
  var ARROW = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2.5 19.5 21 12 17.2 4.5 21Z"/></svg>';
  var $ = function (id) { return document.getElementById(id); };
  var phone = window.matchMedia("(max-width: 767px)");
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  var map = L.map(el, { zoomControl: false, zoomAnimation: !reduce, fadeAnimation: !reduce, markerZoomAnimation: !reduce }).setView([0, 0], 2);
  // OpenStreetMap's own tiles, which need no API key. Their usage policy
  // (https://operations.osmfoundation.org/policies/tiles/) allows only this
  // exact URL, with no {s} subdomain, and a credit that names the licence,
  // which the link to /copyright does. It also requires a Referer on tile
  // requests: setting it on the tiles keeps it whatever the page's own policy.
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    maxZoom: 19,
    referrerPolicy: "strict-origin-when-cross-origin",
  }).addTo(map);
  var box = map.getContainer();
  // Leaflet runs its own 0.25 s marker transition while it zooms; the 10 s glide stands aside for it.
  map.on("zoomstart", function () { box.classList.add("is-zooming"); });
  map.on("zoomend", function () { requestAnimationFrame(function () { requestAnimationFrame(function () { box.classList.remove("is-zooming"); }); }); });

  var routes = {}, routesOk = false, heads = {}, askedHeads = {}, trips = {};
  var markers = {}, last = null, filter = "all", picked = null, follow = false, listOpen = !phone.matches, lastOk = 0;
  // emptyShut: the list was closed because nothing was reporting, not by the dispatcher.
  var emptyShut = false;
  var routeLayer = L.layerGroup().addTo(map);
  var rows = {}, listSig = "", chipSig = "", card = null;

  function h(tag, cls, text) { var n = document.createElement(tag); if (cls) n.className = cls; if (text != null) n.textContent = text; return n; }
  function icon(name) { var s = h("span"); s.innerHTML = '<svg class="icon" aria-hidden="true"><use href="/static/icons.svg#' + name + '"/></svg>'; return s.firstChild; }
  // GTFS writes a colour as six hex digits with no "#". Anything else never reaches a style.
  function hex(c) { return /^[0-9a-fA-F]{6}$/.test(c || "") ? "#" + c : ""; }
  function ink(c) {
    var n = parseInt(c.slice(1), 16), f = function (x) { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); };
    var l = 0.2126 * f(n >> 16) + 0.7152 * f((n >> 8) & 255) + 0.0722 * f(n & 255);
    return (l + 0.05) / 0.05 > 1.05 / (l + 0.05) ? "#000000" : "#FFFFFF";
  }
  function routeName(id) { var r = routes[id]; return r ? r.long_name || r.short_name || id : id || "No route"; }
  function badge(id, big) {
    var r = routes[id], b = h("span", "route" + (big ? " route-lg" : ""), r ? r.short_name || r.id : id ? id : "?");
    if (r && r.hex) { b.style.background = r.hex; b.style.color = ink(r.hex); }
    return b;
  }
  // A report's age from when the server got it, as the server's quiet alerts count it: a phone's clock can be off.
  function ageOf(v) { return Math.max(0, Math.round((Date.now() - Date.parse(v.updated_at)) / 1000)); }
  function ageText(s) { return s < 60 ? s + " s" : Math.floor(s / 60) + " min"; }
  function agoText(s) { return s < 2 ? "just now" : ageText(s) + " ago"; }
  function isQuiet(v) { return ageOf(v) > QUIET; }
  // Open alerts by vehicle, from the alerts endpoint. Going quiet is seen here from the report's age; the others only the server knows.
  var alertBy = {}, KIND = { not_moving: "Not moving", off_route: "Off route", not_started: "Not started" };
  function alertOf(v) { var a = alertBy[v.vehicle_id]; return a && KIND[a.kind] ? a : null; }
  function needs(v) { return isQuiet(v) || !!alertOf(v); }
  function alertText(a) { return KIND[a.kind] + " " + ageText(a.duration_seconds); }
  function compass(b) { return ["North", "North-east", "East", "South-east", "South", "South-west", "West", "North-west"][Math.round((((b % 360) + 360) % 360) / 45) % 8]; }
  function headsign(v) { return heads[v.gtfs_trip_id] || ""; }
  function speed(v) { return Math.round(v.speed * 3.6) + " km/h"; }
  // On a desktop the route chips wrap to more rows when there are many routes. The list starts below the last row,
  // and a fit keeps the vehicles out from under them.
  function chipsH() { return Math.max(36, $("map-chips").offsetHeight); }
  new ResizeObserver(function () { root.style.setProperty("--chips", chipsH() + "px"); }).observe($("map-chips"));
  function pads() { return { paddingTopLeft: [listOpen && !phone.matches ? 352 : 24, phone.matches ? 84 : chipsH() + 48], paddingBottomRight: [picked && !phone.matches ? 384 : 72, phone.matches ? 220 : 48] }; }
  // A rebuilt list, chip row or card keeps the keyboard where it was, or on the fallback when that control is gone.
  function keepFocus(host, build, fallback) {
    var key = host.contains(document.activeElement) ? document.activeElement.dataset.key : null;
    build();
    var again = key && (host.querySelector('[data-key="' + CSS.escape(key) + '"]') || (fallback && fallback()));
    if (again) again.focus({ preventScroll: true });
  }

  /* The schedule: route names and colours, each trip's headsign, and a trip's line and stops when a vehicle is picked.
     The live answer never waits for it, so the names can arrive after the vehicles. */
  var routesAsking = false;
  function loadRoutes() {
    if (routesAsking) return;
    routesAsking = true;
    VT.getJSON(GTFS + "/routes").then(function (d) {
      routesAsking = false;
      routes = {};
      d.routes.forEach(function (r) { r.hex = hex(r.color); routes[r.id] = r; });
      routesOk = true;
      chipSig = listSig = "";
      Object.keys(markers).forEach(function (id) { delete markers[id].route; });
      restyle();
      if (last) { chips(last); list(last); }
      if (card && markers[picked]) { card.key = ""; cardUpdate(markers[picked].v); }
      if (answered && !Object.keys(markers).length) placeOnSchedule();
    }, function () { routesAsking = false; });
  }
  function learnHeadsigns(live) {
    live.vehicles.forEach(function (v) {
      var id = v.route_id;
      if (!id || askedHeads[id]) return;
      askedHeads[id] = true;
      VT.getJSON(GTFS + "/routes/" + encodeURIComponent(id) + "/trips").then(function (d) {
        d.trips.forEach(function (t) { if (t.headsign) heads[t.id] = t.headsign; });
        listSig = "";
        if (last) list(last);
        if (picked && markers[picked]) cardUpdate(markers[picked].v);
      }, function (e) { if (e.message !== "HTTP 404") delete askedHeads[id]; });
    });
  }
  function trip(id) {
    if (!trips[id]) trips[id] = VT.getJSON(GTFS + "/trips/" + encodeURIComponent(id)).catch(function () { delete trips[id]; return null; });
    return trips[id];
  }
  // With no vehicle to fit, the map shows where the schedule's first route runs rather than the whole world.
  var placed = false;
  function placeOnSchedule() {
    var first = Object.keys(routes)[0];
    if (placed || !first) return;
    placed = true;
    VT.getJSON(GTFS + "/routes/" + encodeURIComponent(first) + "/trips").then(function (d) {
      return d.trips.length ? trip(d.trips[0].id) : null;
    }).then(function (t) {
      if (t && t.shape.points.length && !Object.keys(markers).length) map.fitBounds(t.shape.points, { padding: [48, 48], maxZoom: 13, animate: false });
    }, function () {});
  }

  /* Markers */
  function upsert(v, quietly) {
    var m = markers[v.vehicle_id], r = routes[v.route_id];
    if (!m) {
      var mk = L.marker([v.latitude, v.longitude], {
        icon: L.divIcon({ className: "veh-icon", iconSize: [30, 30], iconAnchor: [15, 15], html: '<div class="veh-marker"><span class="veh-ring"></span>' + ARROW + '<span class="veh-route"></span><span class="veh-tag"></span></div>' }),
        keyboard: false, riseOnHover: true,
      }).addTo(map);
      m = markers[v.vehicle_id] = { mk: mk, el: mk.getElement().firstChild, angle: v.bearing || 0, seen: v.reported_at };
      mk.on("click", function () { pick(v.vehicle_id); });
    } else {
      m.mk.setLatLng([v.latitude, v.longitude]);
      if (v.reported_at !== m.seen && !reduce && !quietly && Object.keys(markers).length <= 80) {
        m.el.classList.remove("heard"); void m.el.offsetWidth; m.el.classList.add("heard");
      }
      m.seen = v.reported_at;
    }
    m.v = v;
    if (m.label !== v.label) { m.label = v.label; m.el.querySelector(".veh-tag").textContent = v.label; }
    // The marker itself is plain ink, so a blue or red route is never read as late or early. The route rides along as a tag.
    if (m.route !== v.route_id) {
      m.route = v.route_id;
      var tag = m.el.querySelector(".veh-route");
      tag.textContent = r ? r.short_name || "" : "";
      tag.hidden = !r || !r.short_name;
      tag.style.removeProperty("--c"); tag.style.removeProperty("--ci");
      if (r && r.hex) { tag.style.setProperty("--c", r.hex); tag.style.setProperty("--ci", ink(r.hex)); }
    }
    var quiet = isQuiet(v), attn = quiet || !!alertOf(v);
    // What needs a look is drawn on top, and stays full size when the others shrink to dots.
    if (attn !== m.attn) { m.attn = attn; if (picked !== v.vehicle_id) m.mk.setZIndexOffset(attn ? 1000 : 0); }
    m.el.classList.toggle("quiet", quiet);
    m.el.classList.toggle("attn", attn && !quiet);
    m.el.classList.toggle("no-bearing", v.bearing == null);
    if (v.bearing != null) {
      // Turn the short way round: 350 to 10 degrees is 20 degrees, not 340.
      m.angle += ((v.bearing - m.angle + 540) % 360) - 180;
      m.el.querySelector("svg").style.transform = "rotate(" + m.angle + "deg)";
    }
    m.mk.getElement().style.display = shown(v) ? "" : "none";
  }
  function shown(v) { return filter === "all" || (filter === "attn" ? needs(v) : (v.route_id || "") === filter); }
  function restyle() { Object.keys(markers).forEach(function (id) { upsert(markers[id].v, true); }); }

  // Markers are updated fifty to a frame, so a poll with 200 vehicles never blocks a frame for long.
  function draw(live, firstPaint) {
    last = live;
    var vs = live.vehicles, i = 0;
    if (firstPaint) box.classList.add("no-glide");
    (function step() {
      if (last !== live) return;
      var end = firstPaint ? vs.length : Math.min(vs.length, i + 50);
      for (; i < end; i++) upsert(vs[i], firstPaint);
      if (i < vs.length) requestAnimationFrame(step); else requestAnimationFrame(function () { after(live, firstPaint); });
    })();
  }
  function after(live, firstPaint) {
    if (last !== live) return;
    var ids = {};
    live.vehicles.forEach(function (v) { ids[v.vehicle_id] = true; });
    Object.keys(markers).forEach(function (id) {
      if (ids[id]) return;
      if (picked === id) { $("map-said").textContent = markers[id].v.label + " is no longer on the map."; closeCard(true); }
      map.removeLayer(markers[id].mk); delete markers[id];
    });
    // The chips come first, so the first fit knows how many rows of them there are.
    chips(live);
    if (firstPaint) {
      if (live.count) fit(false); else placeOnSchedule();
      requestAnimationFrame(function () { requestAnimationFrame(function () { box.classList.remove("no-glide"); }); });
    }
    $("map-empty").hidden = live.count > 0;
    $("map-stale").hidden = true; box.classList.remove("stale");
    dense();
    list(live);
    if (picked && markers[picked]) {
      cardUpdate(markers[picked].v);
      if (follow) map.panTo(markers[picked].mk.getLatLng(), { animate: !reduce, duration: POLL / 1000, easeLinearity: 1 });
    }
  }

  function fit(animate) {
    var pts = Object.keys(markers).filter(function (id) { return shown(markers[id].v); }).map(function (id) { return markers[id].mk.getLatLng(); });
    if (!pts.length) return;
    var o = pads(); o.maxZoom = 14; o.animate = animate !== false && !reduce;
    map.fitBounds(L.latLngBounds(pts), o);
  }

  // With many vehicles in a wide view each one is a dot; zoom in and they are markers again. One class, no work per marker.
  function dense() { box.classList.toggle("dense", Object.keys(markers).length > 60 && map.getZoom() < 14); }
  // On its own frame, after Leaflet has finished placing the markers for the new zoom.
  map.on("zoomend", function () { requestAnimationFrame(dense); });

  /* Filter chips: what needs attention, then every route in the answer. */
  function chips(live) {
    var host = $("map-chips"), attn = live.vehicles.filter(needs).length, per = {};
    live.vehicles.forEach(function (v) { var k = v.route_id || ""; per[k] = (per[k] || 0) + 1; });
    // A filter that no vehicle matches any more goes back to All, so the map never stands empty with vehicles on it.
    if (filter === "attn" ? !attn : filter !== "all" && !(filter in per)) { filter = "all"; restyle(); }
    var sig = filter + "|" + attn + "|" + JSON.stringify(per);
    if (sig === chipSig) return;
    chipSig = sig;
    keepFocus(host, function () {
      host.textContent = "";
      function chip(key, label, n, extra) {
        var b = h("button", "chip chip-float" + (extra || ""));
        b.type = "button"; b.dataset.key = key; b.setAttribute("aria-pressed", String(filter === key));
        if (routes[key]) b.appendChild(badge(key));
        b.appendChild(document.createTextNode(label + " "));
        b.appendChild(h("span", "n", String(n)));
        b.addEventListener("click", function () {
          filter = key;
          restyle(); chips(last); list(last); fit(true);
        });
        host.appendChild(b);
      }
      chip("all", "All", live.count);
      if (attn) chip("attn", "Needs attention", attn, " chip-warn");
      Object.keys(per).sort().forEach(function (id) { chip(id, routeName(id), per[id]); });
    });
  }

  /* The vehicle list: grouped by route, what needs attention first. Above 40 vehicles a route opens when asked. */
  var closed = {};
  function rowText(v) {
    var q = isQuiet(v), a = q ? null : alertOf(v), hs = headsign(v);
    if (q || a) return { q: true, sub: (q ? "Quiet " + ageText(ageOf(v)) : alertText(a)) + (hs ? ", to " + hs : ""), age: "" };
    var where = hs ? "to " + hs : v.gtfs_trip_id ? "On a trip" : "No trip";
    return { q: false, sub: where + (v.speed == null ? "" : ", " + speed(v)), age: agoText(ageOf(v)) };
  }
  function list(live) {
    var host = $("fleet-list"), groups = {}, keys = [], folds = live.count > 40;
    $("map-count").textContent = String(live.count);
    live.vehicles.forEach(function (v) {
      var n = needs(v), key = v.route_id || "";
      if (!shown(v)) return;
      (groups[key] = groups[key] || []).push(v);
      if (folds && closed[key] == null) closed[key] = true;
      // Each vehicle's route and name are in the signature, so one that changes route or is renamed moves.
      keys.push(folds && closed[key] ? [key, n] : [key, v.vehicle_id, v.label, n]);
    });
    var sig = JSON.stringify([filter, folds, keys]);
    if (sig === listSig) {
      live.vehicles.forEach(function (v) {
        var r = rows[v.vehicle_id]; if (!r) return;
        var t = rowText(v);
        if (r.sub.textContent !== t.sub) r.sub.textContent = t.sub;
        if (r.age.textContent !== t.age) r.age.textContent = t.age;
      });
      return;
    }
    listSig = sig;
    keepFocus(host, function () {
      rows = {};
      host.textContent = "";
      if (!live.count) { var e = h("div", "empty"); e.appendChild(h("b", "", "No vehicles are reporting")); host.appendChild(e); }
      Object.keys(groups).sort().forEach(function (id) {
        var shut = folds && closed[id], head = h(folds ? "button" : "div", "list-group");
        head.appendChild(badge(id)); head.appendChild(h("span", "", routeName(id)));
        var att = groups[id].filter(needs).length;
        if (att && shut) { var w = h("span", "tone-warn", att + (att === 1 ? " needs" : " need") + " attention"); w.style.fontWeight = "500"; w.style.marginLeft = "auto"; head.appendChild(w); }
        var n = h("span", "muted", String(groups[id].length)); if (!(att && shut)) n.style.marginLeft = "auto"; n.style.fontWeight = "400"; head.appendChild(n);
        if (folds) {
          head.type = "button"; head.dataset.key = "route:" + id; head.setAttribute("aria-expanded", String(!shut));
          var chev = icon("expand_more"); chev.classList.add("icon-sm"); head.appendChild(chev);
          head.addEventListener("click", function () { closed[id] = !closed[id]; list(last); });
        }
        host.appendChild(head);
        if (shut) return;
        groups[id].sort(function (a, b) { return needs(b) - needs(a) || a.label.localeCompare(b.label); }).forEach(function (v) {
          var row = h("button", "list-row"), tx = rowText(v);
          row.type = "button"; row.dataset.key = v.vehicle_id;
          if (picked === v.vehicle_id) row.setAttribute("aria-current", "true");
          var t = h("span", "t"); t.appendChild(h("span", "a", v.label));
          var sub = h("span", "b" + (tx.q ? " tone-warn" : ""), tx.sub); if (tx.q) sub.style.fontWeight = "500";
          t.appendChild(sub); row.appendChild(t);
          var age = h("span", "age" + (tx.q ? " tone-warn" : "")); age.appendChild(h("span", "dot" + (tx.q ? " dot-quiet" : "")));
          var ageTxt = h("span", "", tx.age); age.appendChild(ageTxt);
          if (tx.q) age.style.fontWeight = "500";
          row.appendChild(age);
          row.addEventListener("click", function () { pick(v.vehicle_id); });
          host.appendChild(row);
          rows[v.vehicle_id] = { sub: sub, age: ageTxt, row: row };
        });
      });
    });
    // With nothing reporting the list closes, and on a desktop it opens again when vehicles report, unless the
    // dispatcher has opened or closed it since.
    if (!live.count) { if (listOpen) { setList(false); emptyShut = true; } } else if (emptyShut) { emptyShut = false; if (!phone.matches) setList(true); }
  }

  /* The picked vehicle: its card, its trip's line and stops. */
  var picks = 0, tripAsks = 0;
  function pick(id) {
    var mine = ++picks, ask = ++tripAsks;
    if (picked && markers[picked]) { markers[picked].el.classList.remove("selected"); markers[picked].mk.setZIndexOffset(markers[picked].attn ? 1000 : 0); }
    if (picked && rows[picked]) rows[picked].row.removeAttribute("aria-current");
    picked = id; follow = false; routeLayer.clearLayers();
    var shell = $("map-detail");
    if (!id || !markers[id]) { shell.hidden = true; card = null; return; }
    if (rows[id]) rows[id].row.setAttribute("aria-current", "true");
    var m = markers[id], v = m.v;
    m.el.classList.add("selected");
    m.mk.setZIndexOffset(2000);
    var fromList = $("map-list").contains(document.activeElement);
    if (phone.matches) setList(false);
    // The card answers the click at once; the pan follows on the next frame and the trip's line when the schedule answers.
    detail(v); shell.hidden = false;
    shell.classList.remove("float-in"); void shell.offsetWidth; shell.classList.add("float-in");
    if (fromList && phone.matches) card.close.focus();
    $("map-said").textContent = v.label + ", " + card.word.textContent + ".";
    requestAnimationFrame(function () {
      if (mine !== picks || !markers[id]) return;
      var o = pads(); o.animate = !reduce;
      map.panInside(markers[id].mk.getLatLng(), o);
    });
    showTrip(v, ask);
  }
  // The trip's line and stops, once the schedule answers, unless another pick or a new trip has come first.
  function showTrip(v, ask) {
    if (!v.gtfs_trip_id) return;
    trip(v.gtfs_trip_id).then(function (t) {
      if (!t || ask !== tripAsks || !t.shape.points.length) return;
      var r = routes[v.route_id], dark = getComputedStyle(document.documentElement).colorScheme === "dark";
      L.polyline(t.shape.points, { color: dark ? "#0B0B0C" : "#FFFFFF", weight: 10, interactive: false }).addTo(routeLayer);
      L.polyline(t.shape.points, { color: (r && r.hex) || "#3B6939", weight: 5.5, interactive: false }).addTo(routeLayer);
      t.stops.forEach(function (s) {
        L.marker([s.lat, s.lon], { icon: L.divIcon({ className: "", html: '<div class="stop-marker"></div>', iconSize: [12, 12], iconAnchor: [6, 6] }), keyboard: false })
          .bindTooltip(h("span", "", s.name), { direction: "top", offset: [0, -6] }).addTo(routeLayer);
      });
      if (t.headsign && !heads[t.id]) { heads[t.id] = t.headsign; cardUpdate(markers[v.vehicle_id].v); }
    });
  }

  // The card is built once for each pick and then only its words change, so the keyboard keeps its place on it.
  // A new trip, route or name builds it again.
  function cardKey(v) { return JSON.stringify([v.route_id, v.gtfs_trip_id, v.trip_db_id, v.label]); }
  function detail(v) {
    var host = $("map-detail");
    host.textContent = "";
    card = { id: v.vehicle_id, key: cardKey(v) };
    var head = h("div", "veh-head");
    head.appendChild(badge(v.route_id, true));
    var t = h("div"); t.style.cssText = "flex:1;min-width:0";
    t.appendChild(h("div", "veh-name", v.label));
    card.route = t.appendChild(h("div", "muted"));
    head.appendChild(t);
    var x = card.close = h("button", "icon-btn"); x.type = "button"; x.dataset.key = "close"; x.setAttribute("aria-label", "Close"); x.appendChild(icon("close"));
    x.addEventListener("click", function () { closeCard(false); }); head.appendChild(x);
    host.appendChild(head);

    card.state = h("div", "veh-state");
    card.dot = card.state.appendChild(h("span", "dot"));
    card.word = card.state.appendChild(h("b"));
    card.seen = card.state.appendChild(h("span", "muted"));
    host.appendChild(card.state);

    var dl = h("dl", "kv"); dl.style.margin = "14px 0 16px";
    card.kv = {};
    ["Driver", "Trip", "Speed", "Heading"].forEach(function (k) {
      var w = h("div"); w.appendChild(h("dt", "", k)); card.kv[k] = w.appendChild(h("dd")); dl.appendChild(w);
    });
    host.appendChild(dl);

    var acts = h("div"); acts.style.cssText = "display:flex;gap:8px;flex-wrap:wrap";
    var f = card.follow = h("button", "btn"); f.type = "button"; f.dataset.key = "follow";
    f.appendChild(icon("near_me")); card.followWord = f.appendChild(document.createTextNode(""));
    f.addEventListener("click", function () {
      follow = !follow; followShown();
      if (follow && markers[picked]) map.panTo(markers[picked].mk.getLatLng(), { animate: !reduce });
    });
    acts.appendChild(f);
    if (v.trip_db_id != null) { var a = h("a", "btn btn-outline", "Open trip"); a.dataset.key = "trip"; a.href = "/admin/trips/" + v.trip_db_id; acts.appendChild(a); }
    var e = h("a", "btn", "Vehicle"); e.dataset.key = "vehicle"; e.href = "/admin/vehicles/" + encodeURIComponent(v.vehicle_id) + "/edit"; acts.appendChild(e);
    host.appendChild(acts);
    cardUpdate(v);
  }
  function followShown() {
    if (!card) return;
    card.follow.className = "btn " + (follow ? "btn-primary" : "btn-tonal");
    card.follow.setAttribute("aria-pressed", String(follow));
    card.followWord.textContent = follow ? "Following" : "Follow";
  }
  function cardUpdate(v) {
    if (!card || card.id !== v.vehicle_id) return;
    if (card.key !== cardKey(v)) {
      routeLayer.clearLayers();
      keepFocus($("map-detail"), function () { detail(v); }, function () { return card.close; });
      showTrip(v, ++tripAsks);
      return;
    }
    var q = isQuiet(v), al = q ? null : alertOf(v), warn = q || !!al, hs = headsign(v);
    card.route.textContent = routeName(v.route_id) + (hs ? " to " + hs : "");
    card.state.classList.toggle("warn", warn);
    card.dot.className = "dot" + (warn ? " dot-quiet" : "");
    card.word.className = warn ? "tone-warn" : "";
    card.word.textContent = q ? "Quiet" : al ? KIND[al.kind] + " for " + ageText(al.duration_seconds) : "Reporting";
    card.seen.textContent = "Last report " + agoText(ageOf(v));
    card.kv.Driver.textContent = v.driver_name || "Not signed in";
    card.kv.Trip.textContent = v.gtfs_trip_id || "Route only";
    card.kv.Speed.textContent = v.speed == null ? "Not known" : speed(v);
    card.kv.Heading.textContent = v.bearing == null ? "Not known" : compass(v.bearing);
    followShown();
  }

  /* Controls */
  function setList(open) { listOpen = open; $("map-list").hidden = !open; $("map-list-open").hidden = open; }
  setList(listOpen);
  $("map-list-close").addEventListener("click", function () { emptyShut = false; setList(false); $("map-list-open").focus(); });
  $("map-list-open").addEventListener("click", function () { emptyShut = false; if (phone.matches) pick(null); setList(true); $("map-list-close").focus(); });
  $("map-in").addEventListener("click", function () { map.zoomIn(); });
  $("map-out").addEventListener("click", function () { map.zoomOut(); });
  $("map-fit").addEventListener("click", function () { follow = false; followShown(); fit(true); });
  $("map-full").addEventListener("click", function () { if (document.fullscreenElement) document.exitFullscreen(); else if (root.requestFullscreen) root.requestFullscreen().catch(function () {}); });
  document.addEventListener("fullscreenchange", function () { $("map-full").setAttribute("aria-pressed", String(!!document.fullscreenElement)); map.invalidateSize(); });
  window.addEventListener("resize", function () { setTimeout(function () { map.invalidateSize(); }, 320); });
  map.on("dragstart", function () { if (follow) { follow = false; followShown(); } });
  // Closing the card hands the keyboard back to the vehicle's row, or, when the list is shut or the vehicle has
  // gone, to the list button or the first chip.
  function closeCard(gone) {
    var inCard = $("map-detail").contains(document.activeElement), row = rows[picked] && rows[picked].row;
    pick(null);
    if (!inCard) return;
    var to = row && !gone && !$("map-list").hidden ? row : !$("map-list-open").hidden ? $("map-list-open") : $("map-chips").querySelector("button");
    (to || box).focus();
  }
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape" && picked && !document.querySelector("dialog[open]")) closeCard(false);
  });
  $("map-retry").addEventListener("click", function (ev) { ev.preventDefault(); poll(); });

  /* Polling. This page asks for the live answer itself, so the shell's pulse takes its news from here. */
  VT.ownsPulse = true;
  var first = true, polls = 0, asked = 0, answered = 0, alertsAsked = 0, alertsAnswered = 0, alertSig = "[]";
  // Alerts change slowly, so they are asked for on every third poll, on their own: the positions never wait for
  // them, and an older answer that comes late is dropped.
  function askAlerts(forPoll) {
    var mine = ++alertsAsked;
    VT.getJSON(el.dataset.alertsUrl).then(function (a) {
      if (mine < alertsAnswered) return;
      alertsAnswered = mine;
      var by = {};
      a.alerts.forEach(function (x) { if (x.trip && !by[x.trip.vehicle_id]) by[x.trip.vehicle_id] = x; });
      alertBy = by;
      if (VT.alerts) VT.alerts(a);
      var sig = JSON.stringify(Object.keys(by).map(function (id) { return [id, by[id].kind]; }));
      // Positions already drawn without these alerts are drawn again for what needs attention now.
      if (sig !== alertSig && answered >= forPoll && last) { restyle(); chips(last); list(last); if (picked && markers[picked]) cardUpdate(markers[picked].v); }
      alertSig = sig;
    }, function () {});
  }
  function poll() {
    if (document.hidden) return;
    if (!routesOk) loadRoutes();
    var mine = ++asked;
    if (polls++ % 3 === 0) askAlerts(mine);
    // Only the newest answer is drawn. A failure of the request is "no answer"; an error drawing it is not.
    VT.getJSON(el.dataset.liveUrl).then(function (live) {
      if (mine < answered) return;
      answered = mine;
      lastOk = Date.now(); draw(live, first); first = false;
      learnHeadsigns(live);
      if (VT.heard) VT.heard(live);
    }, function () {
      if (mine < answered) return;
      answered = mine;
      if (VT.lost) VT.lost(!lastOk);
      $("map-stale-text").textContent = lastOk ? "No answer from the server. Showing positions from " + new Date(lastOk).toLocaleTimeString("en-GB") + "." : "No answer from the server yet.";
      $("map-stale").hidden = false; $("map-empty").hidden = true; box.classList.add("stale");
      if (last) list(last);
      if (!lastOk) {
        placeOnSchedule();
        var w = h("div", "empty"); w.appendChild(h("b", "", "Waiting for the server")); w.appendChild(h("span", "", "Vehicles show here as soon as it answers."));
        $("fleet-list").textContent = ""; $("fleet-list").appendChild(w); $("map-count").textContent = "";
      }
    });
  }
  poll(); setInterval(poll, POLL);
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) return;
    first = !Object.keys(markers).length;
    box.classList.add("no-glide"); poll();
    setTimeout(function () { box.classList.remove("no-glide"); }, 400);
  });
})();
