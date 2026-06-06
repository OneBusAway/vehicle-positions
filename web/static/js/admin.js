const mockVehicles = [
  {
    id: "SEA-101",
    label: "Bus 101",
    lat: 47.6101,
    lng: -122.3426,
    route: "Rapid E Line",
    status: "active",
    corridor: "Downtown to Ballard",
    stop: "3rd Ave & Pine St",
  },
  {
    id: "SEA-108",
    label: "Bus 108",
    lat: 47.6206,
    lng: -122.3201,
    route: "Route 8",
    status: "active",
    corridor: "Capitol Hill Crosstown",
    stop: "Denny Way & Broadway",
  },
  {
    id: "SEA-214",
    label: "Bus 214",
    lat: 47.5989,
    lng: -122.3347,
    route: "South Lake Loop",
    status: "active",
    corridor: "Pioneer Square Connector",
    stop: "Jackson St Transit Hub",
  },
  {
    id: "SEA-305",
    label: "Bus 305",
    lat: 47.6677,
    lng: -122.3826,
    route: "Rapid E Line",
    status: "idle",
    corridor: "Northwest Layover",
    stop: "Ballard Ave NW",
  },
  {
    id: "SEA-417",
    label: "Bus 417",
    lat: 47.6267,
    lng: -122.3561,
    route: "South Lake Loop",
    status: "active",
    corridor: "Seattle Center Spur",
    stop: "Queen Anne Ave N",
  },
  {
    id: "SEA-522",
    label: "Bus 522",
    lat: 47.5884,
    lng: -122.3023,
    route: "Route 8",
    status: "idle",
    corridor: "Mount Baker Relief",
    stop: "Rainier Ave S",
  },
];

const mockCorridors = [
  {
    name: "Rapid E Line",
    color: "#0f766e",
    points: [
      [47.6101, -122.3426],
      [47.6205, -122.3492],
      [47.6362, -122.3563],
      [47.6516, -122.3752],
      [47.6677, -122.3826],
    ],
  },
  {
    name: "Route 8",
    color: "#f59e0b",
    points: [
      [47.5884, -122.3023],
      [47.6002, -122.3119],
      [47.6117, -122.3174],
      [47.6206, -122.3201],
      [47.6312, -122.3225],
    ],
  },
  {
    name: "South Lake Loop",
    color: "#0284c7",
    points: [
      [47.5989, -122.3347],
      [47.6072, -122.3324],
      [47.6202, -122.3384],
      [47.6267, -122.3561],
    ],
  },
];

function makeMarkerIcon(status) {
  const markerClass = status === "active" ? "bus-marker bus-marker--active" : "bus-marker bus-marker--idle";
  return L.divIcon({
    className: "",
    html: `<div class="${markerClass}">
      <span class="bus-marker__pulse"></span>
      <span class="bus-marker__icon">&#128652;</span>
    </div>`,
    iconSize: [42, 42],
    iconAnchor: [21, 21],
    popupAnchor: [0, -18],
  });
}

function buildPopup(vehicle) {
  const badgeClass = vehicle.status === "active"
    ? "map-popup__badge map-popup__badge--active"
    : "map-popup__badge map-popup__badge--idle";

  return `<div class="map-popup">
    <div class="map-popup__header">
      <div>
        <div class="map-popup__title">&#128652; ${vehicle.label}</div>
        <div style="font-size:12px;color:#64748b;margin-top:2px;">${vehicle.id}</div>
      </div>
      <span class="${badgeClass}">${vehicle.status}</span>
    </div>
    <div class="map-popup__meta">
      <div><strong>Route</strong> ${vehicle.route}</div>
      <div><strong>Corridor</strong> ${vehicle.corridor}</div>
      <div><strong>Nearest stop</strong> ${vehicle.stop}</div>
    </div>
  </div>`;
}

function initMap() {
  const el = document.getElementById("main-map");
  if (!el) return;
  const map = L.map("main-map", {
    zoomControl: false,
    scrollWheelZoom: true,
  }).setView([47.6062, -122.3321], 13);

  L.tileLayer("https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png", {
    attribution: "&copy; OpenStreetMap contributors &copy; CARTO",
    maxZoom: 19,
  }).addTo(map);

  L.control.zoom({ position: "bottomright" }).addTo(map);

  mockCorridors.forEach(corridor => {
    L.polyline(corridor.points, {
      color: corridor.color,
      weight: 5,
      opacity: 0.82,
      lineCap: "round",
    })
      .addTo(map)
      .bindTooltip(corridor.name, {
        direction: "top",
        offset: [0, -4],
        opacity: 0.95,
      });
  });

  mockVehicles.forEach(v => {
    L.marker([v.lat, v.lng], { icon: makeMarkerIcon(v.status) })
      .addTo(map)
      .bindPopup(buildPopup(v));
  });

  const activeCount = mockVehicles.filter(vehicle => vehicle.status === "active").length;
  const activeCountEl = document.getElementById("fleet-active-count");
  const routeCountEl = document.getElementById("route-count");

  if (activeCountEl) activeCountEl.textContent = String(activeCount);
  if (routeCountEl) routeCountEl.textContent = String(mockCorridors.length);
}

// Auto-initialize the map if this page has the map container
if (document.getElementById("main-map")) {
  initMap();
}
