package org.onebusaway.vehicletracker.engine

import org.onebusaway.vehicletracker.service.LocationFix

/**
 * One trip's running judgement. Each fix is projected with the latest on-route judgement as its
 * hint, which keeps a loop or an out-and-back on the right pass. An off-route fix is judged and
 * shown like any other, but it never becomes that baseline: as the server's
 * `Session.LatestMatched` puts it, an off-route point is exactly the position the next point must
 * not be judged against. A trip with no geometry to judge against is judged not at all, and every
 * fix answers null.
 */
class AdherenceTracker(geometry: TripGeometry?) {
    private val evaluator = geometry?.let(AdherenceEvaluator::of)
    private var latestOnRoute: Adherence? = null

    fun onFix(fix: LocationFix): Adherence? =
        evaluator?.evaluate(fix, latestOnRoute)?.also { if (it.isOnRoute) latestOnRoute = it }
}
