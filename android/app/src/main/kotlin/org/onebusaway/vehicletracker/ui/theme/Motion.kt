package org.onebusaway.vehicletracker.ui.theme

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.ui.unit.dp
import kotlin.math.sign

/**
 * The springs of Material's motion schemes that the app uses. material3 1.4.0 keeps `MotionScheme` internal, so
 * the values are copied from its `StandardMotionTokens`; on material3 1.5, where it is public, these become
 * `MaterialTheme.motionScheme.defaultSpatialSpec()` and `defaultEffectsSpec()`. The expressive ones are copied
 * from `ExpressiveMotionTokens` at material3 1.5.0-rc01, and become `MotionScheme.expressive()`'s.
 */
internal object AppMotion {
    /** For what moves or changes size, such as the problem line opening under the status card. */
    fun <T> defaultSpatialSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 0.9f, stiffness = 700f)

    /** For colour and opacity, such as the status card changing tone. It never overshoots. */
    fun <T> defaultEffectsSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 1f, stiffness = 1600f)

    /** The quicker of the two, for a swap the driver asked for, such as the departure board changing direction. */
    fun <T> fastEffectsSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 1f, stiffness = 3800f)

    /** The slowest, for a change of colour the driver is meant to see happen, such as the welcome's vehicle going live. */
    fun <T> slowEffectsSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 1f, stiffness = 800f)

    /**
     * The expressive scheme's spring for what moves, which settles a little past its place and comes back: for a
     * picture's parts, such as a welcome card rising in, not for a control the driver works.
     */
    fun <T> expressiveDefaultSpatialSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 0.8f, stiffness = 380f)

    /** The quicker and bouncier one, for something small landing, such as the check on a welcome card. */
    fun <T> expressiveFastSpatialSpec(): FiniteAnimationSpec<T> = spring(dampingRatio = 0.6f, stiffness = 800f)
}

/** Material's emphasized decelerate curve, copied for the same reason as [AppMotion]; on material3 1.5 it is public. */
internal val EmphasizedDecelerate = CubicBezierEasing(0.05f, 0.7f, 0.1f, 1f)

private const val SWITCH_MILLIS = 300
private const val OUT_MILLIS = 90
private const val IN_MILLIS = SWITCH_MILLIS - OUT_MILLIS

/** How far a setup screen slides as it comes in or goes. */
internal val SharedAxisSlide = 30.dp

/**
 * Material's shared axis X, for the steps of choosing a trip: a short slide, [slidePx] of it, under a quick
 * cross-fade, the old screen fading out before the new one fades in. The incoming screen slides [towards] the
 * start going forward and the end going back, so the motion follows the reading direction.
 */
internal fun AnimatedContentTransitionScope<*>.sharedAxisIn(
    towards: AnimatedContentTransitionScope.SlideDirection,
    slidePx: Int,
): EnterTransition =
    slideIntoContainer(towards, sharedAxisSlideSpec()) { full -> full.sign * slidePx } + fadeIn(sharedAxisFadeInSpec())

/** The outgoing half of [sharedAxisIn]. */
internal fun AnimatedContentTransitionScope<*>.sharedAxisOut(
    towards: AnimatedContentTransitionScope.SlideDirection,
    slidePx: Int,
): ExitTransition =
    slideOutOfContainer(towards, sharedAxisSlideSpec()) { full -> full.sign * slidePx } + fadeOut(sharedAxisFadeOutSpec())

/**
 * The three timings [sharedAxisIn] and [sharedAxisOut] are made of, for a screen that moves its own layers along the
 * axis rather than swapping content: the welcome keeps its three screens composed, so a step composes nothing.
 */
internal fun <T> sharedAxisSlideSpec(): FiniteAnimationSpec<T> = tween(SWITCH_MILLIS, easing = EmphasizedDecelerate)

/** The incoming screen's fade, which waits for the outgoing one's. */
internal fun <T> sharedAxisFadeInSpec(): FiniteAnimationSpec<T> = tween(IN_MILLIS, delayMillis = OUT_MILLIS)

/** The outgoing screen's fade. */
internal fun <T> sharedAxisFadeOutSpec(): FiniteAnimationSpec<T> = tween(OUT_MILLIS)

/** Material's fade through, for a change of task rather than a step, such as starting the trip. */
internal fun fadeThroughIn(): EnterTransition = fadeIn(sharedAxisFadeInSpec())

/** The outgoing half of [fadeThroughIn]. */
internal fun fadeThroughOut(): ExitTransition = fadeOut(sharedAxisFadeOutSpec())
