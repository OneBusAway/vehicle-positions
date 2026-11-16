package org.onebusaway.vehicletracker.ui.help

import androidx.annotation.DrawableRes
import androidx.annotation.StringRes
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.engine.AdherenceStatus
import org.onebusaway.vehicletracker.ui.theme.StatusTone
import org.onebusaway.vehicletracker.ui.tracking.StatusCardModel

/** The status card's own words for a status: minutes, read as the card reads them ("6 min late"), or its string. */
internal sealed interface CardWords {
    data class Minutes(val minutes: Int) : CardWords
    data class Text(@StringRes val id: Int) : CardWords
}

/** One answer the status card gives, as Help explains it: the card's words, icon and colour, then one plain line. */
internal enum class CardStatusHelp(val words: CardWords, @StringRes val meaning: Int, val tone: StatusTone, @DrawableRes val icon: Int) {
    ON_TIME(CardWords.Minutes(0), R.string.help_status_on_time, StatusTone.ON_TIME, R.drawable.ic_check_circle),
    LATE(CardWords.Minutes(6), R.string.help_status_late, StatusTone.LATE, R.drawable.ic_hourglass_bottom),
    EARLY(CardWords.Minutes(-3), R.string.help_status_early, StatusTone.EARLY, R.drawable.ic_speed),

    // The card is red or blue by which way the run is off; Help shows it red, as a run started too early.
    OFF_SCHEDULE(
        CardWords.Text(R.string.tracking_adherence_off_schedule),
        R.string.help_status_off_schedule,
        StatusTone.EARLY,
        R.drawable.ic_event_busy,
    ),
    OFF_ROUTE(
        CardWords.Text(R.string.tracking_adherence_off_route_short),
        R.string.help_status_off_route,
        StatusTone.NEUTRAL,
        R.drawable.ic_wrong_location,
    ),
    WAITING_FOR_GPS(
        CardWords.Text(R.string.tracking_adherence_waiting_for_gps),
        R.string.help_status_waiting_for_gps,
        StatusTone.NEUTRAL,
        R.drawable.ic_location_searching,
    ),
    NO_SCHEDULE(
        CardWords.Text(R.string.tracking_adherence_no_schedule),
        R.string.help_status_no_schedule,
        StatusTone.NEUTRAL,
        R.drawable.ic_event_busy,
    ),
}

/** What can show under the status card, in its place, or as a notification, as Help explains it. */
internal enum class ProblemHelp(@StringRes val title: Int, @StringRes val meaning: Int, @DrawableRes val icon: Int) {
    NO_CONNECTION(R.string.tracking_problem_no_network, R.string.help_problem_no_network, R.drawable.ic_cloud_off),
    NO_GPS(R.string.tracking_problem_no_gps, R.string.help_problem_no_gps, R.drawable.ic_location_disabled),
    CLOCK_WRONG(R.string.tracking_problem_clock_skew, R.string.help_problem_clock_skew, R.drawable.ic_schedule),
    REPORTS_REFUSED(R.string.tracking_problem_reports_refused, R.string.help_problem_reports_refused, R.drawable.ic_no_transfer),
    SIGNED_OUT(R.string.tracking_signed_out_title, R.string.help_problem_signed_out, R.drawable.ic_lock),

    /** The notification posted when reports stop until the app is opened. It has no line under the card. */
    REPORTS_PAUSED(R.string.tracking_notification_resume_title, R.string.help_problem_reports_paused, R.drawable.ic_notifications_active),
}

/** The Help row for what the status card shows. A new status does not compile until it has one. */
internal fun cardStatusHelp(model: StatusCardModel): CardStatusHelp = when (model) {
    is StatusCardModel.Adherence -> cardStatusHelp(model.status)
    is StatusCardModel.OffSchedule -> CardStatusHelp.OFF_SCHEDULE
    is StatusCardModel.OffRoute -> CardStatusHelp.OFF_ROUTE
    is StatusCardModel.WaitingForGps -> CardStatusHelp.WAITING_FOR_GPS
    StatusCardModel.NoSchedule -> CardStatusHelp.NO_SCHEDULE
}

internal fun cardStatusHelp(status: AdherenceStatus): CardStatusHelp = when (status) {
    AdherenceStatus.ON_TIME -> CardStatusHelp.ON_TIME
    AdherenceStatus.LATE -> CardStatusHelp.LATE
    AdherenceStatus.EARLY -> CardStatusHelp.EARLY
    AdherenceStatus.OFF_SCHEDULE -> CardStatusHelp.OFF_SCHEDULE
    AdherenceStatus.OFF_ROUTE -> CardStatusHelp.OFF_ROUTE
}

/** The Help row for a problem the trip can have; null for none. A new problem does not compile until it has one. */
internal fun problemHelp(problem: TrackingProblem): ProblemHelp? = when (problem) {
    TrackingProblem.NONE -> null
    TrackingProblem.NO_NETWORK -> ProblemHelp.NO_CONNECTION
    TrackingProblem.NO_GPS -> ProblemHelp.NO_GPS
    TrackingProblem.CLOCK_SKEW -> ProblemHelp.CLOCK_WRONG
    TrackingProblem.REPORTS_REFUSED -> ProblemHelp.REPORTS_REFUSED
    TrackingProblem.AUTH_EXPIRED -> ProblemHelp.SIGNED_OUT
}

/** Where a row sits in its group, which decides its corners: round on the group's outside, tight between rows. */
internal enum class GroupPlace { FIRST, MIDDLE, LAST }

private fun groupPlace(index: Int, size: Int): GroupPlace = when (index) {
    0 -> GroupPlace.FIRST
    size - 1 -> GroupPlace.LAST
    else -> GroupPlace.MIDDLE
}

internal enum class HelpSection(@StringRes val title: Int) {
    STATUS(R.string.help_section_status),
    PROBLEMS(R.string.help_section_problems),
    ABOUT(R.string.help_section_about),
}

/** Which fact an About row shows. The values are read when the page is drawn: the server's can arrive a little late. */
internal enum class AboutFact(@StringRes val label: Int, @DrawableRes val icon: Int) {
    VERSION(R.string.help_version, R.drawable.ic_info),
    SERVER(R.string.help_server, R.drawable.ic_dns),
}

/**
 * One item of the Help page. [key] is unique on the page and [contentType] lets the list reuse a row only for another
 * of its kind.
 */
internal sealed interface HelpItem {
    val key: String
    val contentType: String

    data object WelcomeAgain : HelpItem {
        override val key = "welcome"
        override val contentType = "welcome"
    }

    data class Section(val section: HelpSection) : HelpItem {
        override val key = "section-${section.name}"
        override val contentType = "section"
    }

    data class Status(val help: CardStatusHelp, val place: GroupPlace) : HelpItem {
        override val key = "status-${help.name}"
        override val contentType = "status"
    }

    data class Problem(val help: ProblemHelp, val place: GroupPlace) : HelpItem {
        override val key = "problem-${help.name}"
        override val contentType = "problem"
    }

    data class About(val fact: AboutFact, val place: GroupPlace) : HelpItem {
        override val key = "about-${fact.name}"
        override val contentType = "about"
    }

    data object Share : HelpItem {
        override val key = "share"
        override val contentType = "share"
    }
}

/** The whole page, top to bottom, built once. */
internal val helpPage: List<HelpItem> = buildList {
    add(HelpItem.WelcomeAgain)
    add(HelpItem.Section(HelpSection.STATUS))
    CardStatusHelp.entries.forEachIndexed { i, help -> add(HelpItem.Status(help, groupPlace(i, CardStatusHelp.entries.size))) }
    add(HelpItem.Section(HelpSection.PROBLEMS))
    ProblemHelp.entries.forEachIndexed { i, help -> add(HelpItem.Problem(help, groupPlace(i, ProblemHelp.entries.size))) }
    add(HelpItem.Section(HelpSection.ABOUT))
    AboutFact.entries.forEachIndexed { i, fact -> add(HelpItem.About(fact, groupPlace(i, AboutFact.entries.size))) }
    add(HelpItem.Share)
}
