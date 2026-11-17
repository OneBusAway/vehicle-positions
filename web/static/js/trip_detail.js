// The trip page's map and slider. The page itself is drawn by the server; this
// script fetches the trail from the trip locations API and replays it.
(function () {
  const el = document.getElementById("trip-map");
  const dataEl = document.getElementById("trip-data");
  if (!el || !dataEl || typeof L === "undefined") return;

  const cfg = JSON.parse(dataEl.textContent);
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // The canvas renderer draws a trail of thousands of reports as a few
  // shapes instead of thousands of SVG nodes.
  const map = L.map(el, {
    zoomControl: false,
    preferCanvas: true,
    zoomAnimation: !reduceMotion,
    fadeAnimation: !reduceMotion,
    markerZoomAnimation: !reduceMotion,
  }).setView([0, 0], 2);
  // The same OpenStreetMap tiles, credit and Referer as the live map
  // (admin.js), which explains the tile policy.
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    maxZoom: 19,
    referrerPolicy: "strict-origin-when-cross-origin",
  }).addTo(map);
  L.control.zoom({ position: "bottomright" }).addTo(map);

  if (cfg.shape.length > 1) {
    L.polyline(cfg.shape, { color: "#94a3b8", weight: 6, opacity: 0.7, interactive: false }).addTo(map);
  }
  cfg.stops.forEach(s => {
    L.circleMarker([s.lat, s.lon], { radius: 5, color: "#475569", weight: 2, fillColor: "#ffffff", fillOpacity: 1 })
      .bindTooltip(s.name)
      .addTo(map);
  });

  const trailLayer = L.layerGroup().addTo(map);
  const marker = L.marker([0, 0], {
    icon: L.divIcon({
      className: "",
      html: '<div class="bus-marker bus-marker--active"><span class="bus-marker__icon">&#128652;</span></div>',
      iconSize: [42, 42],
      iconAnchor: [21, 21],
    }),
    keyboard: false,
  });

  const slider = document.getElementById("trip-slider");
  const playButton = document.getElementById("trip-play");
  const timeLabel = document.getElementById("trip-time");
  const speedButtons = Array.from(document.querySelectorAll("[data-speed]"));

  let clock;
  try {
    clock = new Intl.DateTimeFormat("en-GB", { timeZone: cfg.timezone, hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
  } catch (e) {
    clock = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
  }

  let points = [];
  // index is the report the marker is at, or the last one it has passed
  // while playing; playClock is the playback time in unix seconds.
  let index = 0;
  let playClock = 0;
  let speed = 60;
  let playing = false;
  let lastFrame = 0;

  function latLng(p) {
    return [p.latitude, p.longitude];
  }

  function isGap(a, b) {
    return b.reported_at - a.reported_at > cfg.gap_seconds;
  }

  // drawTrail draws the trail in the route's colour over a dark casing that
  // keeps a pale colour visible, with a dashed line across each gap. Leaflet
  // simplifies each line for the zoom level it is drawn at, so the slider,
  // which works from the reports themselves, still reaches every one.
  function drawTrail() {
    trailLayer.clearLayers();
    let run = [];
    const flush = () => {
      if (run.length > 1) {
        L.polyline(run, { color: "#0f172a", weight: 7, opacity: 0.25, interactive: false }).addTo(trailLayer);
        L.polyline(run, { color: cfg.color, weight: 4, opacity: 0.95, interactive: false }).addTo(trailLayer);
      }
      run = [];
    };
    points.forEach((p, i) => {
      if (i > 0 && isGap(points[i - 1], p)) {
        flush();
        L.polyline([latLng(points[i - 1]), latLng(p)], { color: cfg.color, weight: 3, opacity: 0.9, dashArray: "6 8", interactive: false }).addTo(trailLayer);
      }
      run.push(latLng(p));
    });
    flush();
  }

  function describe(i) {
    const p = points[i];
    let text = clock.format(new Date(p.reported_at * 1000)) + ", report " + (i + 1) + " of " + points.length;
    if (p.speed != null) text += ", " + Math.round(p.speed) + " m/s";
    return text;
  }

  function showReport(i) {
    index = i;
    slider.value = String(i);
    const text = describe(i);
    timeLabel.textContent = text;
    slider.setAttribute("aria-valuetext", text);
  }

  function keepInView(ll) {
    if (!map.getBounds().contains(ll)) map.panTo(ll, { animate: !reduceMotion });
  }

  // goTo moves the marker to report i and the playback time with it.
  function goTo(i) {
    showReport(i);
    playClock = points[i].reported_at;
    marker.setLatLng(latLng(points[i]));
    keepInView(latLng(points[i]));
  }

  function setPlaying(on) {
    playing = on;
    playButton.textContent = on ? "Pause" : "Play";
    if (on) {
      lastFrame = performance.now();
      requestAnimationFrame(frame);
    }
  }

  // frame moves the playback time on by speed times the time since the last
  // frame, and the marker to where the vehicle was then: between the two
  // reports either side, except across a gap, where nothing says where it was.
  function frame(now) {
    if (!playing) return;
    playClock += ((now - lastFrame) / 1000) * speed;
    lastFrame = now;
    let i = index;
    while (i < points.length - 1 && points[i + 1].reported_at <= playClock) i++;
    if (i >= points.length - 1) {
      goTo(points.length - 1);
      setPlaying(false);
      return;
    }
    if (i !== index) showReport(i);
    const a = points[i];
    const b = points[i + 1];
    let ll = latLng(a);
    if (!isGap(a, b) && b.reported_at > a.reported_at) {
      const f = (playClock - a.reported_at) / (b.reported_at - a.reported_at);
      ll = [a.latitude + (b.latitude - a.latitude) * f, a.longitude + (b.longitude - a.longitude) * f];
    }
    marker.setLatLng(ll);
    keepInView(ll);
    requestAnimationFrame(frame);
  }

  playButton.addEventListener("click", () => {
    if (playing) {
      setPlaying(false);
      return;
    }
    if (index >= points.length - 1) goTo(0);
    setPlaying(true);
  });

  speedButtons.forEach(b => {
    b.addEventListener("click", () => {
      speed = Number(b.dataset.speed);
      speedButtons.forEach(o => o.setAttribute("aria-pressed", String(o === b)));
    });
  });

  slider.addEventListener("input", () => goTo(Number(slider.value)));

  // The gap list is drawn by the server and replaced on each refresh of an
  // active trip, so its buttons are handled where they are clicked.
  document.addEventListener("click", e => {
    const button = e.target.closest("[data-gap]");
    if (!button || !points.length) return;
    const i = Math.min(Number(button.dataset.gap), points.length - 1);
    setPlaying(false);
    goTo(i);
    const next = points[Math.min(i + 1, points.length - 1)];
    map.fitBounds([latLng(points[i]), latLng(next)], { padding: [60, 60], maxZoom: 17, animate: !reduceMotion });
    el.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "center" });
  });

  // setPoints shows a new trail, keeping the marker on the newest report if
  // it was there, as it is when the page opens.
  function setPoints(fresh) {
    const following = !points.length || index >= points.length - 1;
    points = fresh;
    drawTrail();
    const empty = points.length === 0;
    document.getElementById("trip-empty").classList.toggle("hidden", !empty);
    slider.disabled = empty;
    playButton.disabled = empty;
    if (empty) return;
    slider.max = String(points.length - 1);
    if (!map.hasLayer(marker)) marker.addTo(map);
    if (following && !playing) {
      goTo(points.length - 1);
    } else {
      showReport(Math.min(index, points.length - 1));
    }
  }

  async function fetchTrail() {
    const res = await fetch(cfg.trail_url, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    return res.json();
  }

  // refreshSections replaces the parts of the page the server works out from
  // the trail (the header, the stops and the gaps) with a fresh copy of the
  // page, so the stop times and gaps are worked out in one place.
  async function refreshSections() {
    const res = await fetch(location.href, { headers: { Accept: "text/html" } });
    if (!res.ok) return;
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    ["trip-header", "trip-stops", "trip-gaps"].forEach(id => {
      const fresh = doc.getElementById(id);
      const old = document.getElementById(id);
      if (fresh && old) old.replaceWith(fresh);
    });
  }

  let timer = null;

  async function refresh() {
    try {
      const data = await fetchTrail();
      const fresh = data.points || [];
      const last = points[points.length - 1];
      const newest = fresh[fresh.length - 1];
      const changed = fresh.length !== points.length || (newest && last && newest.reported_at !== last.reported_at);
      const ended = data.trip && data.trip.status !== "active";
      if (changed) setPoints(fresh);
      if (changed || ended) await refreshSections();
      if (ended) {
        cfg.active = false;
        clearInterval(timer);
        document.getElementById("trip-live-note")?.remove();
      }
    } catch (e) {
      console.error("trip refresh failed", e);
    }
  }

  async function load() {
    try {
      const data = await fetchTrail();
      setPoints(data.points || []);
      const bounds = points.map(latLng).concat(cfg.shape);
      if (bounds.length) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 16, animate: false });
    } catch (e) {
      console.error("trail load failed", e);
    }
    // An active trip's page fetches the trail again every 10 seconds while
    // it is in view, as the live map does.
    if (cfg.active) {
      timer = setInterval(() => {
        if (!document.hidden) refresh();
      }, 10000);
      document.addEventListener("visibilitychange", () => {
        if (!document.hidden && cfg.active) refresh();
      });
    }
  }

  load();
})();
