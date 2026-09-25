package org.onebusaway.vehicletracker.engine

import org.onebusaway.vehicletracker.service.LocationFix

/**
 * One trip's running judgement: each fix is evaluated against the one before it, so the previous
 * match can keep a loop or an out-and-back on the right pass. A trip with no geometry to judge
 * against is judged not at all, and every fix answers null.
 */
class AdherenceTracker(geometry: TripGeometry?) {
    private val evaluator = geometry?.let(AdherenceEvaluator::of)
    private var previous: Adherence? = null

    fun onFix(fix: LocationFix): Adherence? = evaluator?.evaluate(fix, previous)?.also { previous = it }
}
