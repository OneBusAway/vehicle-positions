package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.spring

/**
 * The springs of Material's standard motion scheme that the app uses. material3 1.4.0 keeps `MotionScheme`
 * internal, so the values are copied from its `StandardMotionTokens`; on material3 1.5, where it is public,
 * these become `MaterialTheme.motionScheme.defaultSpatialSpec()` and `defaultEffectsSpec()`.
 */
internal object AppMotion {
    /** For what moves or changes size, such as the problem line opening under the status card. */
    fun <T> defaultSpatialSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 0.9f, stiffness = 700f)

    /** For colour and opacity, such as the status card changing tone. It never overshoots. */
    fun <T> defaultEffectsSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 1f, stiffness = 1600f)
}
