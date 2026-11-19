// The trip page's replay: the trail over the route on a map, and a timeline
// drawn as the Line, with stops as ticks, gaps hatched and speed above it. The
// page itself is drawn by the server; this script fetches the trail from the
// trip locations API and replays it. The range input under the line keeps the
// timeline usable from the keyboard and by a screen reader.
(function () {
  const el = document.getElementById("trip-map");
  const dataEl = document.getElementById("trip-data");
  if (!el || !dataEl || typeof L === "undefined") return;
  const byId = id => document.getElementById(id);

  const cfg = JSON.parse(dataEl.textContent);
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  // The route's colour is the feed's; it reaches a style only as six hex digits.
  const color = /^#[0-9a-fA-F]{6}$/.test(cfg.color) ? cfg.color : "#3B6939";

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
  // (map.js), which explains the tile policy.
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    maxZoom: 19,
    referrerPolicy: "strict-origin-when-cross-origin",
  }).addTo(map);
  byId("trip-in").addEventListener("click", () => map.zoomIn());
  byId("trip-out").addEventListener("click", () => map.zoomOut());

  if (cfg.shape.length > 1) {
    L.polyline(cfg.shape, { color: "#8E918F", weight: 4, opacity: 0.7, interactive: false }).addTo(map);
  }
  const stopIcon = L.divIcon({ className: "", html: '<div class="stop-marker"></div>', iconSize: [12, 12], iconAnchor: [6, 6] });
  cfg.stops.forEach(s => {
    L.marker([s.lat, s.lon], { icon: stopIcon, keyboard: false })
      // A stop name is the feed's text, never markup: a string tooltip
      // would be parsed as HTML.
      .bindTooltip(Object.assign(document.createElement("span"), { textContent: s.name }), { direction: "top", offset: [0, -6] })
      .addTo(map);
  });

  const trailLayer = L.layerGroup().addTo(map);
  const marker = L.marker([0, 0], {
    icon: L.divIcon({
      className: "veh-icon",
      html: '<div class="veh-marker selected"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2.5 19.5 21 12 17.2 4.5 21Z"/></svg></div>',
      iconSize: [30, 30],
      iconAnchor: [15, 15],
    }),
    keyboard: false,
    zIndexOffset: 1000,
  });

  const slider = byId("trip-slider");
  const playButton = byId("trip-play");
  const timeLabel = byId("trip-time");
  const metaLabel = byId("trip-meta");
  const line = document.querySelector("#trip-recorder .the-line");
  const thumb = byId("trip-thumb");
  const ticks = byId("trip-ticks");
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

  // along is where a time sits on the timeline, as a percentage of its
  // width: the line runs from the first report to the last.
  function along(seconds) {
    const first = points[0].reported_at;
    const span = Math.max(1, points[points.length - 1].reported_at - first);
    return Math.min(100, Math.max(0, ((seconds - first) / span) * 100));
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
        L.polyline(run, { color: "#1F1F1F", weight: 9, opacity: 0.55, interactive: false }).addTo(trailLayer);
        L.polyline(run, { color: color, weight: 5, interactive: false }).addTo(trailLayer);
      }
      run = [];
    };
    points.forEach((p, i) => {
      if (i > 0 && isGap(points[i - 1], p)) {
        flush();
        L.polyline([latLng(points[i - 1]), latLng(p)], { color: color, weight: 4, dashArray: "4 8", interactive: false }).addTo(trailLayer);
      }
      run.push(latLng(p));
    });
    flush();
  }

  // drawTimeline draws what the line carries besides the vehicle: a tick for
  // each stop the server says the vehicle passed (the rows of the Stops
  // table), a hatched stretch for each gap, and the speed above. The speed
  // line stops at a gap and starts again after it: nothing is known between.
  function drawTimeline() {
    ticks.textContent = "";
    document.querySelectorAll("#trip-stops tr[data-passed]").forEach(row => {
      const tick = document.createElement("span");
      tick.className = "stop";
      tick.style.left = along(Number(row.dataset.passed)) + "%";
      tick.title = row.dataset.stop;
      ticks.appendChild(tick);
    });
    // A reduce rather than spreading the speeds into Math.max, which runs out of stack on a trail of over about 100,000 reports.
    const fastest = points.reduce((most, p) => Math.max(most, p.speed || 0), 10);
    let path = "";
    let area = "";
    let pen = false;
    points.forEach((p, i) => {
      const x = (along(p.reported_at) * 10).toFixed(1);
      if (i > 0 && isGap(points[i - 1], p)) {
        const from = along(points[i - 1].reported_at);
        const gap = document.createElement("span");
        gap.className = "gap";
        gap.style.left = from + "%";
        gap.style.width = along(p.reported_at) - from + "%";
        gap.title = "No report for " + Math.round((p.reported_at - points[i - 1].reported_at) / 60) + " min";
        ticks.appendChild(gap);
        if (pen) area += "L" + (from * 10).toFixed(1) + " 44";
        pen = false;
      }
      if (p.speed == null) return;
      const y = (42 - (p.speed / fastest) * 36).toFixed(1);
      path += (pen ? "L" : "M") + x + " " + y;
      area += (pen ? "L" : "M" + x + " 44L") + x + " " + y;
      pen = true;
    });
    if (pen) area += "L" + (along(points[points.length - 1].reported_at) * 10).toFixed(1) + " 44";
    const speedChart = byId("trip-speed");
    speedChart.innerHTML = '<path class="area"></path><path class="line" style="stroke-width:1.5" vector-effect="non-scaling-stroke"></path>';
    speedChart.firstChild.setAttribute("d", area);
    speedChart.lastChild.setAttribute("d", path);
  }

  // heading is which way the vehicle faced at report i: the phone's bearing,
  // or the way from the report before when the phone gave none.
  function heading(i) {
    const p = points[i];
    if (p.bearing != null) return p.bearing;
    const from = points[i - 1];
    if (!from || (from.latitude === p.latitude && from.longitude === p.longitude)) return null;
    const east = (p.longitude - from.longitude) * Math.cos((p.latitude * Math.PI) / 180);
    return (Math.atan2(east, p.latitude - from.latitude) * 180) / Math.PI;
  }

  function showReport(i) {
    index = i;
    slider.value = String(i);
    const p = points[i];
    const time = clock.format(new Date(p.reported_at * 1000));
    let meta = "report " + (i + 1) + " of " + points.length;
    if (p.speed != null) meta += ", " + Math.round(p.speed * 3.6) + " km/h";
    timeLabel.textContent = time;
    metaLabel.textContent = meta;
    slider.setAttribute("aria-valuetext", time + ", " + meta);
    const icon = marker.getElement();
    if (icon) {
      const turn = heading(i);
      icon.firstChild.classList.toggle("no-bearing", turn == null);
      if (turn != null) icon.querySelector("svg").style.transform = "rotate(" + turn + "deg)";
    }
  }

  function keepInView(ll) {
    if (!map.getBounds().contains(ll)) map.panTo(ll, { animate: !reduceMotion });
  }

  // goTo moves the marker to report i and the playback time with it.
  function goTo(i) {
    playClock = points[i].reported_at;
    marker.setLatLng(latLng(points[i]));
    thumb.style.left = along(playClock) + "%";
    showReport(i);
    keepInView(latLng(points[i]));
  }

  function setPlaying(on) {
    playing = on;
    playButton.querySelector("span").textContent = on ? "Pause" : "Play";
    playButton.querySelector("use").setAttribute("href", "/static/icons.svg#" + (on ? "pause" : "play_arrow"));
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
    thumb.style.left = along(playClock) + "%";
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

  slider.addEventListener("input", () => {
    setPlaying(false);
    goTo(Number(slider.value));
  });

  // The line is spaced by time and the range input by report, so a pointer
  // on the line goes to the report nearest the time under it.
  function seek(e) {
    const box = line.getBoundingClientRect();
    const first = points[0].reported_at;
    const at = first + Math.min(1, Math.max(0, (e.clientX - box.left) / box.width)) * (points[points.length - 1].reported_at - first);
    let nearest = 0;
    points.forEach((p, i) => {
      if (Math.abs(p.reported_at - at) < Math.abs(points[nearest].reported_at - at)) nearest = i;
    });
    setPlaying(false);
    goTo(nearest);
  }
  line.addEventListener("pointerdown", e => {
    if (!points.length) return;
    line.setPointerCapture(e.pointerId);
    seek(e);
  });
  line.addEventListener("pointermove", e => {
    if (line.hasPointerCapture(e.pointerId)) seek(e);
  });

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
    byId("trip-empty").hidden = !empty;
    slider.disabled = empty;
    playButton.disabled = empty;
    if (empty) return;
    drawTimeline();
    slider.max = String(points.length - 1);
    if (!map.hasLayer(marker)) marker.addTo(map);
    if (following && !playing) {
      goTo(points.length - 1);
    } else {
      showReport(Math.min(index, points.length - 1));
      thumb.style.left = along(playClock) + "%";
    }
  }

  async function fetchTrail() {
    const res = await fetch(cfg.trail_url, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error("HTTP " + res.status);
    return res.json();
  }

  // refreshSections replaces the parts of the page the server works out from
  // the trail (the header, the facts, the stops and the gaps) with a fresh
  // copy of the page, so the stop times and gaps are worked out in one place.
  // It returns whether it did, so a failed attempt is made again on the next
  // refresh.
  async function refreshSections() {
    const res = await fetch(location.href, { headers: { Accept: "text/html" } });
    if (!res.ok) return false;
    const doc = new DOMParser().parseFromString(await res.text(), "text/html");
    // An expired session is sent to the sign-in page, which has no header.
    if (!doc.getElementById("trip-header")) return false;
    ["trip-header", "trip-facts", "trip-stops", "trip-gaps"].forEach(id => {
      const fresh = doc.getElementById(id);
      const old = document.getElementById(id);
      if (fresh && old) old.replaceWith(fresh);
    });
    if (points.length) drawTimeline();
    return true;
  }

  let timer = null;
  // refreshing stops the timer and the tab coming back into view from
  // starting a second refresh before the first ends, so an older answer can
  // never land after a newer one.
  let refreshing = false;
  // sectionsStale is set while the page's sections are behind the trail.
  let sectionsStale = false;

  async function refresh() {
    if (refreshing) return;
    refreshing = true;
    try {
      const data = await fetchTrail();
      const fresh = data.points || [];
      const last = points[points.length - 1];
      const newest = fresh[fresh.length - 1];
      const changed = fresh.length !== points.length || (newest && last && newest.reported_at !== last.reported_at);
      const ended = data.trip && data.trip.status !== "active";
      if (changed) setPoints(fresh);
      if (changed || ended) sectionsStale = true;
      if (sectionsStale) sectionsStale = !(await refreshSections());
      // The page keeps polling until it shows the trip as ended.
      if (ended && !sectionsStale) {
        cfg.active = false;
        clearInterval(timer);
        document.getElementById("trip-live-note")?.remove();
      }
    } catch (e) {
      console.error("trip refresh failed", e);
    } finally {
      refreshing = false;
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
