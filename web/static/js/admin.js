(function () {
  const el = document.getElementById("main-map");
  if (!el || typeof L === "undefined") return;

  const map = L.map("main-map", { zoomControl: false }).setView([0, 0], 2);
  L.tileLayer("https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png", {
    attribution: "&copy; OpenStreetMap contributors &copy; CARTO",
    maxZoom: 19,
  }).addTo(map);
  L.control.zoom({ position: "bottomright" }).addTo(map);

  // busIcon returns the shared divIcon markup used for every marker (live
  // fleet vehicles and trail start/end points). There is only one visual
  // style now that the map no longer distinguishes idle vehicles.
  function busIcon() {
    return L.divIcon({
      className: "",
      html: '<div class="bus-marker bus-marker--active">' +
        '<span class="bus-marker__pulse"></span>' +
        '<span class="bus-marker__icon">&#128652;</span>' +
        "</div>",
      iconSize: [42, 42],
      iconAnchor: [21, 21],
      popupAnchor: [0, -18],
    });
  }

  // ageText renders a human-friendly "how long ago" string from a unix
  // timestamp (seconds). Falls back to "unknown" for missing/invalid input.
  function ageText(unixSeconds) {
    if (!unixSeconds && unixSeconds !== 0) return "unknown";
    const deltaMs = Date.now() - unixSeconds * 1000;
    const deltaSec = Math.max(0, Math.round(deltaMs / 1000));
    if (deltaSec < 60) return deltaSec + "s ago";
    const deltaMin = Math.round(deltaSec / 60);
    if (deltaMin < 60) return deltaMin + "m ago";
    const deltaHr = Math.round(deltaMin / 60);
    return deltaHr + "h ago";
  }

  // metaRow builds a single "<strong>label</strong> value" row using DOM
  // APIs only, so any server-supplied text lands via textContent.
  function metaRow(label, value) {
    const row = document.createElement("div");
    const strong = document.createElement("strong");
    strong.textContent = label;
    row.appendChild(strong);
    row.appendChild(document.createTextNode(" " + value));
    return row;
  }

  // popupHtml builds a vehicle marker popup's DOM tree from live-feed data.
  // Every server-provided string goes through textContent/createTextNode —
  // never innerHTML — so a malicious label/driver name can't inject markup.
  function popupHtml(v) {
    const wrap = document.createElement("div");
    wrap.className = "map-popup";

    const header = document.createElement("div");
    header.className = "map-popup__header";

    const titleWrap = document.createElement("div");
    const title = document.createElement("div");
    title.className = "map-popup__title";
    title.textContent = "\u{1F68C} " + (v.label || v.vehicle_id);
    const subtitle = document.createElement("div");
    subtitle.style.fontSize = "12px";
    subtitle.style.color = "#64748b";
    subtitle.style.marginTop = "2px";
    subtitle.textContent = String(v.vehicle_id);
    titleWrap.appendChild(title);
    titleWrap.appendChild(subtitle);
    header.appendChild(titleWrap);
    wrap.appendChild(header);

    const meta = document.createElement("div");
    meta.className = "map-popup__meta";
    meta.appendChild(metaRow("Route", v.route_id || "—"));
    meta.appendChild(metaRow("Driver", v.driver_name || "—"));
    meta.appendChild(metaRow("Speed", v.speed != null ? Math.round(v.speed) + " m/s" : "—"));
    meta.appendChild(metaRow("Updated", ageText(v.reported_at)));
    wrap.appendChild(meta);

    return wrap;
  }

  async function fetchJSON(url) {
    const res = await fetch(url, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    return res.json();
  }

  // --- live mode ---
  let markers = new Map();
  let fitted = false;
  let timer = null;

  async function refresh(url) {
    try {
      const data = await fetchJSON(url);
      drawVehicles(data.vehicles || []);
      updateSidebar(data.vehicles || []);
    } catch (e) {
      console.error("live refresh failed", e);
    }
  }

  function startLive(url) {
    refresh(url);
    timer = setInterval(() => {
      if (!document.hidden) refresh(url);
    }, 10000);
    document.addEventListener("visibilitychange", () => {
      if (!document.hidden) refresh(url);
    });
  }

  function drawVehicles(vehicles) {
    const seen = new Set();
    vehicles.forEach(v => {
      seen.add(v.vehicle_id);
      const ll = [v.latitude, v.longitude];
      if (markers.has(v.vehicle_id)) {
        markers.get(v.vehicle_id).setLatLng(ll).setPopupContent(popupHtml(v));
      } else {
        markers.set(v.vehicle_id, L.marker(ll, { icon: busIcon() }).addTo(map).bindPopup(popupHtml(v)));
      }
    });
    for (const [id, m] of markers) {
      if (!seen.has(id)) {
        map.removeLayer(m);
        markers.delete(id);
      }
    }
    document.getElementById("empty-banner")?.classList.toggle("hidden", vehicles.length > 0);
    if (!fitted && vehicles.length) {
      map.fitBounds(vehicles.map(v => [v.latitude, v.longitude]), { padding: [40, 40], maxZoom: 15 });
      fitted = true;
    }
    const routes = new Set(vehicles.map(v => v.route_id).filter(Boolean));
    setText("stat-active", vehicles.length);
    setText("stat-routes", routes.size);
  }

  // updateSidebar rebuilds the #fleet-list rows from scratch on every
  // refresh. The fleet is small enough that a full rebuild is simpler (and
  // safer against stale nodes) than diffing, and every field is set via
  // textContent so server strings can never become markup.
  function updateSidebar(vehicles) {
    const list = document.getElementById("fleet-list");
    if (!list) return;
    list.textContent = "";

    if (!vehicles.length) {
      const empty = document.createElement("p");
      empty.className = "text-xs text-slate-400";
      empty.textContent = "No vehicles reporting.";
      list.appendChild(empty);
      return;
    }

    vehicles.forEach(v => {
      const row = document.createElement("div");
      row.className = "flex items-center gap-3 rounded-xl bg-slate-50/80 p-2.5";

      const icon = document.createElement("span");
      icon.className = "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-teal-600 text-sm text-white";
      icon.textContent = "\u{1F68C}";
      row.appendChild(icon);

      const info = document.createElement("div");
      info.className = "min-w-0 flex-1";

      const label = document.createElement("p");
      label.className = "truncate text-sm font-semibold text-slate-900";
      label.textContent = v.label || v.vehicle_id;
      info.appendChild(label);

      const sub = document.createElement("p");
      sub.className = "truncate text-xs text-slate-400";
      const routeText = v.route_id || "No route";
      const driverText = v.driver_name || "Unassigned";
      sub.textContent = routeText + " · " + driverText;
      info.appendChild(sub);

      row.appendChild(info);

      const dot = document.createElement("span");
      dot.className = "h-2 w-2 shrink-0 rounded-full bg-emerald-500";
      row.appendChild(dot);

      list.appendChild(row);
    });
  }

  function setText(id, v) {
    const n = document.getElementById(id);
    if (n) n.textContent = String(v);
  }

  // --- trail mode ---
  async function renderTrail(url) {
    try {
      const data = await fetchJSON(url);
      // Header first so a trip with no recorded points still shows its
      // summary alongside the empty banner.
      renderTripHeader(data.trip);
      const pts = (data.points || []).map(p => [p.latitude, p.longitude]);
      if (!pts.length) {
        document.getElementById("empty-banner")?.classList.remove("hidden");
        return;
      }
      L.polyline(pts, { color: "#0f766e", weight: 5, opacity: 0.85 }).addTo(map);
      L.marker(pts[0], { icon: busIcon() }).addTo(map).bindPopup(trailPopup("Start", data.trip));
      L.marker(pts[pts.length - 1], { icon: busIcon() }).addTo(map).bindPopup(trailPopup("End", data.trip));
      map.fitBounds(pts, { padding: [40, 40] });
    } catch (e) {
      console.error("trail load failed", e);
    }
  }

  // trailPopup builds a Start/End marker popup from trip metadata via DOM
  // APIs only.
  function trailPopup(kind, trip) {
    const wrap = document.createElement("div");
    wrap.className = "map-popup";

    const header = document.createElement("div");
    header.className = "map-popup__header";
    const title = document.createElement("div");
    title.className = "map-popup__title";
    title.textContent = kind;
    header.appendChild(title);
    wrap.appendChild(header);

    const meta = document.createElement("div");
    meta.className = "map-popup__meta";
    if (trip) {
      meta.appendChild(metaRow("Vehicle", trip.vehicle_label || trip.vehicle_id || "—"));
      meta.appendChild(metaRow("Driver", trip.driver_name || "—"));
      meta.appendChild(metaRow("Route", trip.route_id || "—"));
    }
    wrap.appendChild(meta);

    return wrap;
  }

  // renderTripHeader replaces the fleet sidebar with a summary of the trip
  // being viewed, built entirely via DOM APIs.
  function renderTripHeader(trip) {
    const list = document.getElementById("fleet-list");
    if (!list || !trip) return;
    list.textContent = "";
    setText("fleet-title", "Trip Detail");

    const card = document.createElement("div");
    card.className = "space-y-2";

    const title = document.createElement("p");
    title.className = "text-sm font-semibold text-slate-900";
    title.textContent = trip.vehicle_label || trip.vehicle_id || "Trip";
    card.appendChild(title);

    card.appendChild(metaRow("Driver", trip.driver_name || "—"));
    card.appendChild(metaRow("Route", trip.route_id || "—"));
    card.appendChild(metaRow("Status", trip.status || "—"));
    card.appendChild(metaRow("Started", trip.start_time || "—"));
    if (trip.end_time) card.appendChild(metaRow("Ended", trip.end_time));

    list.appendChild(card);
  }

  // Mode dispatch runs last so every let-bound module state (markers, fitted,
  // timer) is initialized before startLive's first refresh touches it.
  const tripUrl = el.dataset.tripUrl;
  if (tripUrl) {
    renderTrail(tripUrl);
  } else {
    startLive(el.dataset.liveUrl);
  }
})();
