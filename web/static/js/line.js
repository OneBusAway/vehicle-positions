// The Overview, kept current. Every 10 seconds the page is fetched again and
// what changed is taken from the fresh copy: each vehicle slides to where the
// server now places it along its route's line, numbers count on to their new
// values, and the cards that list things are swapped. The server works every
// number out, so the page is right without this script, as of when it loaded.
(function () {
  "use strict";
  var VT = window.VT || {};
  var all = function (root, sel) { return Array.prototype.slice.call(root.querySelectorAll(sel)); };
  var keys = function (root) { return all(root, "[data-line]").map(function (l) { return l.dataset.line; }).join("|"); };
  if (!document.querySelector("[data-live]")) return;

  function moveVehicles(line, fresh) {
    var seen = {};
    all(fresh, ".veh").forEach(function (v) {
      seen[v.dataset.veh] = true;
      var el = all(line, ".veh").filter(function (o) { return o.dataset.veh === v.dataset.veh; })[0];
      if (!el) { line.appendChild(document.importNode(v, true)); return; }
      el.className = v.className;
      el.style.left = v.style.left;
    });
    all(line, ".veh").forEach(function (el) { if (!seen[el.dataset.veh]) el.remove(); });
  }

  function refresh() {
    if (document.hidden) return;
    fetch(location.href, { headers: { Accept: "text/html" } }).then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status);
      return res.text();
    }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html");
      // A signed-out session gets the sign-in page, which has none of these.
      if (!doc.querySelector("[data-live]")) return;
      var routes = document.querySelector("[data-live-routes]"), freshRoutes = doc.querySelector("[data-live-routes]");
      if (keys(routes) !== keys(freshRoutes)) {
        routes.replaceWith(document.importNode(freshRoutes, true));
      } else {
        all(routes, "[data-line]").forEach(function (line, i) {
          var row = all(freshRoutes, ".line-row")[i], old = line.parentNode;
          moveVehicles(line, row.querySelector("[data-line]"));
          old.firstElementChild.innerHTML = row.firstElementChild.innerHTML;
          old.lastElementChild.innerHTML = row.lastElementChild.innerHTML;
        });
      }
      all(document, "[data-live]").forEach(function (el) {
        var fresh = doc.querySelector('[data-live="' + el.dataset.live + '"]');
        if (!fresh) return;
        if (el.classList.contains("count-up")) VT.setCount(el, Number(fresh.dataset.count));
        else if (el.innerHTML !== fresh.innerHTML) el.innerHTML = fresh.innerHTML;
      });
    }).catch(function () {});
  }
  setInterval(refresh, 10000);
  document.addEventListener("visibilitychange", refresh);
})();
