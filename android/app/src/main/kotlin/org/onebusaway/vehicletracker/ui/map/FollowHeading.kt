package org.onebusaway.vehicletracker.ui.map

/**
 * The heading the map follows. A fix with no course keeps the last known one: Android has none
 * when `Location.hasBearing()` is false, which `LocationFix.bearing` carries as null, and steering
 * to north on every such fix spins the map. The last heading is a far better guess. iOS keeps
 * `lastCourse` for the same reason, against Core Location's -1.
 */
class FollowHeading {
    /** Degrees clockwise from north; north until the first fix that has a course. */
    var degrees = 0.0
        private set

    fun update(course: Double?): Double {
        if (course != null && course in 0.0..360.0) degrees = course
        return degrees
    }
}
