package org.onebusaway.vehicletracker.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import org.onebusaway.vehicletracker.data.ApiError
import org.onebusaway.vehicletracker.data.AuthRepository
import org.onebusaway.vehicletracker.data.MyTrip
import org.onebusaway.vehicletracker.data.MyTripsPage
import org.onebusaway.vehicletracker.data.MyTripsSource
import java.time.Duration
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale
import javax.inject.Inject

/** The server's own default page. */
internal const val MY_TRIPS_PAGE_SIZE = 50

/** The page to load as the list ends. [generation] tells one asked for before a refresh from one asked for after it. */
data class TripsPageRequest(val offset: Int, val generation: Int)

enum class MyTripsLoad { LOADING, FAILED, LOADED }

/**
 * My trips as it loads, a page at a time (the design's sheet 10): the trips the driver has ended, newest first. Every
 * change goes through here, so tests drive it without a screen. iOS's `MyTripsList`.
 */
data class MyTripsList(
    val load: MyTripsLoad = MyTripsLoad.LOADING,
    /** Ended trips, newest first. A running one is left out, as the empty page says: "A trip shows here after you end it." */
    val trips: List<MyTrip> = emptyList(),
    val hasMore: Boolean = false,
    /** A pull to refresh failed: the list stays, with a banner saying so. */
    val refreshFailed: Boolean = false,
    /** The next page failed: the end of the list says so, with Try again. */
    val moreFailed: Boolean = false,
    /** How many trips the server has sent, a running one included: where the next page starts. */
    private val received: Int = 0,
    private val generation: Int = 0,
) {
    /** The page to load as the list ends, if there is one to load now. */
    val nextPage: TripsPageRequest?
        get() = if (load == MyTripsLoad.LOADED && hasMore && !moreFailed) TripsPageRequest(received, generation) else null

    /** Try again, after the first page failed: the spinner again. */
    fun reloaded(): MyTripsList = copy(load = MyTripsLoad.LOADING)

    /** The first page, from the first load, Try again or a refresh. It replaces whatever was there. */
    fun receivedFirst(page: MyTripsPage): MyTripsList = copy(
        load = MyTripsLoad.LOADED,
        trips = page.trips.filter { it.end != null },
        hasMore = page.hasMore,
        refreshFailed = false,
        moreFailed = false,
        received = page.trips.size,
        generation = generation + 1,
    )

    /** A refresh that fails keeps the list; a first load takes the screen. */
    fun firstFailed(): MyTripsList = if (load == MyTripsLoad.LOADED) copy(refreshFailed = true) else copy(load = MyTripsLoad.FAILED)

    /**
     * A trip started since the first page pushes the rest one place down, so the next page can repeat the last trip
     * already here; it shows once. A page asked for before a refresh is dropped.
     */
    fun received(page: MyTripsPage, request: TripsPageRequest): MyTripsList {
        if (request != nextPage) return this
        val shown = trips.mapTo(HashSet()) { it.id }
        return copy(
            trips = trips + page.trips.filter { it.end != null && it.id !in shown },
            received = received + page.trips.size,
            hasMore = page.hasMore,
        )
    }

    fun failed(request: TripsPageRequest): MyTripsList = if (request == nextPage) copy(moreFailed = true) else this

    /** Try again at the end of the list. */
    fun retriedMore(): MyTripsList = copy(moreFailed = false)
}

/** The trips that started on [date], in the phone's time zone. */
data class MyTripsDay(val date: LocalDate, val trips: List<MyTrip>)

/** [trips], newest first, under the days they started on in [zone]. */
internal fun myTripsDays(trips: List<MyTrip>, zone: ZoneId): List<MyTripsDay> {
    val days = mutableListOf<MyTripsDay>()
    for (trip in trips) {
        val date = trip.start.atZone(zone).toLocalDate()
        val last = days.lastOrNull()
        if (last?.date == date) days[days.lastIndex] = last.copy(trips = last.trips + trip) else days += MyTripsDay(date, listOf(trip))
    }
    return days
}

/** A day's title: Today, Yesterday, or the date by [format], with its year when it is not [today]'s. */
internal fun dayTitle(
    date: LocalDate,
    today: LocalDate,
    todayTitle: String,
    yesterdayTitle: String,
    format: (date: LocalDate, withYear: Boolean) -> String,
): String = when (date) {
    today -> todayTitle
    today.minusDays(1) -> yesterdayTitle
    else -> format(date, date.year != today.year)
}

/**
 * Today's date in the phone's time zone, and again each midnight, so Today and Yesterday move on while the page is left
 * open (Aaron on #687). [now] is read again at each, so a change of time zone is seen at the next.
 */
internal fun todays(now: () -> ZonedDateTime): Flow<LocalDate> = flow {
    while (true) {
        val at = now()
        emit(at.toLocalDate())
        delay(Duration.between(at, at.toLocalDate().plusDays(1).atStartOfDay(at.zone)).toMillis())
    }
}.distinctUntilChanged()

/** What a row of My trips shows: the route's badge, where the trip went, and when it ran and on which vehicle. */
data class MyTripRow(
    /** The route's short name, or its id when the schedule no longer has it. */
    val badge: String,
    val color: String,
    val textColor: String,
    /** The headsign, or the route's name when the trip has none. */
    val title: String?,
    val start: String,
    val end: String,
    val vehicle: String,
)

internal fun myTripRow(trip: MyTrip, zone: ZoneId, locale: Locale): MyTripRow {
    val route = trip.route
    val time = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale)
    return MyTripRow(
        badge = route?.shortName?.ifEmpty { null } ?: trip.routeId,
        color = route?.color.orEmpty(),
        textColor = route?.textColor.orEmpty(),
        title = trip.headsign.ifEmpty { null } ?: route?.longName?.ifEmpty { null },
        start = time.format(trip.start.atZone(zone)),
        end = trip.end?.let { time.format(it.atZone(zone)) }.orEmpty(),
        vehicle = trip.vehicleId,
    )
}

/** My trips' list as it loads, and whether a pull to refresh is out. A 401 ends the session, as on the pickers. */
@HiltViewModel
class MyTripsViewModel @Inject constructor(
    private val trips: MyTripsSource,
    private val auth: AuthRepository,
) : ViewModel() {
    private val _list = MutableStateFlow(MyTripsList())
    val list: StateFlow<MyTripsList> = _list.asStateFlow()

    private val _refreshing = MutableStateFlow(false)
    val refreshing: StateFlow<Boolean> = _refreshing.asStateFlow()

    private val _sessionEnded = MutableStateFlow(false)
    val sessionEnded: StateFlow<Boolean> = _sessionEnded.asStateFlow()

    /** The next page being loaded, so the end of the list asks for it once. */
    private var loadingMore: TripsPageRequest? = null

    init {
        loadFirst()
    }

    fun onRetry() {
        _list.update { it.reloaded() }
        loadFirst()
    }

    fun onRefresh() {
        if (_refreshing.value || _list.value.load != MyTripsLoad.LOADED) return
        _refreshing.value = true
        loadFirst()
    }

    fun onLoadMore(request: TripsPageRequest) {
        if (request != _list.value.nextPage || request == loadingMore) return
        loadingMore = request
        viewModelScope.launch {
            trips.page(MY_TRIPS_PAGE_SIZE, request.offset)
                .onSuccess { page -> _list.update { it.received(page, request) } }
                .onFailure { e -> if (!endedSession(e)) _list.update { it.failed(request) } }
            if (loadingMore == request) loadingMore = null
        }
    }

    fun onRetryMore() = _list.update { it.retriedMore() }

    private fun loadFirst() {
        viewModelScope.launch {
            trips.page(MY_TRIPS_PAGE_SIZE, 0)
                .onSuccess { page -> _list.update { it.receivedFirst(page) } }
                .onFailure { e -> if (!endedSession(e)) _list.update { it.firstFailed() } }
            _refreshing.value = false
        }
    }

    // The client has already tried a refresh. The tokens go before the screen goes, as on the pickers.
    private suspend fun endedSession(e: Throwable): Boolean {
        if (e !is ApiError.Unauthorized) return false
        auth.endSessionLocally()
        _sessionEnded.value = true
        return true
    }
}
