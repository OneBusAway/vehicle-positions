package org.onebusaway.vehicletracker.ui.settings

import android.text.format.DateFormat
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import java.util.Locale
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.MyTrip
import org.onebusaway.vehicletracker.ui.components.AttentionBanner
import org.onebusaway.vehicletracker.ui.components.DelayedSpinner
import org.onebusaway.vehicletracker.ui.components.EmptyState
import org.onebusaway.vehicletracker.ui.components.GroupTitle
import org.onebusaway.vehicletracker.ui.components.groupPlace
import org.onebusaway.vehicletracker.ui.components.groupRow
import org.onebusaway.vehicletracker.ui.routes.RouteBadge
import java.time.LocalDate
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter

/**
 * My trips (the design's sheet 10): the trips the driver has ended, newest first under Today, Yesterday and their dates,
 * a page at a time as the list ends. Nothing opens from a row. iOS's `MyTripsView`.
 */
@Composable
fun MyTripsScreen(onBack: () -> Unit, onSessionEnded: () -> Unit, viewModel: MyTripsViewModel = hiltViewModel()) {
    val list by viewModel.list.collectAsStateWithLifecycle()
    val refreshing by viewModel.refreshing.collectAsStateWithLifecycle()
    val sessionEnded by viewModel.sessionEnded.collectAsStateWithLifecycle()
    // Not a tap: it fires once the server has turned the session away.
    LaunchedEffect(sessionEnded) { if (sessionEnded) onSessionEnded() }
    val today by remember { todays { ZonedDateTime.now() } }.collectAsStateWithLifecycle(LocalDate.now())
    MyTripsContent(
        list = list,
        refreshing = refreshing,
        today = today,
        onBack = onBack,
        onRetry = viewModel::onRetry,
        onRefresh = viewModel::onRefresh,
        onLoadMore = viewModel::onLoadMore,
        onRetryMore = viewModel::onRetryMore,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun MyTripsContent(
    list: MyTripsList,
    refreshing: Boolean,
    today: LocalDate,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    onRefresh: () -> Unit,
    onLoadMore: (TripsPageRequest) -> Unit,
    onRetryMore: () -> Unit,
    zone: ZoneId = ZoneId.systemDefault(),
) {
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        SettingsPageBar(R.string.my_trips_title, onBack, scrollBehavior)
        when (list.load) {
            MyTripsLoad.LOADING -> DelayedSpinner()
            MyTripsLoad.FAILED -> EmptyState(
                icon = R.drawable.ic_cloud_off,
                title = stringResource(R.string.my_trips_error_title),
                body = stringResource(R.string.my_trips_error_detail),
                action = stringResource(R.string.my_trips_retry),
                onAction = onRetry,
            )
            MyTripsLoad.LOADED -> PullToRefreshBox(isRefreshing = refreshing, onRefresh = onRefresh, modifier = Modifier.fillMaxSize()) {
                if (list.trips.isEmpty() && !list.hasMore) {
                    // Scrolls, so a pull works here too: a trip may have ended since.
                    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp)) {
                        if (list.refreshFailed) RefreshFailed(onRefresh)
                        EmptyState(
                            icon = R.drawable.ic_history,
                            title = stringResource(R.string.my_trips_empty_title),
                            body = stringResource(R.string.my_trips_empty_detail),
                        )
                    }
                } else {
                    Trips(list, today, zone, onRefresh, onLoadMore, onRetryMore)
                }
            }
        }
    }
}

@Composable
private fun Trips(
    list: MyTripsList,
    today: LocalDate,
    zone: ZoneId,
    onRefresh: () -> Unit,
    onLoadMore: (TripsPageRequest) -> Unit,
    onRetryMore: () -> Unit,
) {
    val days = remember(list.trips, zone) { myTripsDays(list.trips, zone) }
    val todayTitle = stringResource(R.string.my_trips_today)
    val yesterdayTitle = stringResource(R.string.my_trips_yesterday)
    val locale = LocalConfiguration.current.locales[0]
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 32.dp)) {
        if (list.refreshFailed) item(key = "refresh-failed") { RefreshFailed(onRefresh) }
        for (day in days) {
            item(key = "day-${day.date}") {
                GroupTitle(dayTitle(day.date, today, todayTitle, yesterdayTitle) { date, withYear -> dateTitle(date, withYear, locale) })
            }
            itemsIndexed(day.trips, key = { _, trip -> trip.id }) { index, trip ->
                TripRow(trip, zone, Modifier.groupRow(groupPlace(index, day.trips.size)))
            }
        }
        val next = list.nextPage
        if (next != null) {
            item(key = "more") {
                // The pickers' spinner, while the next page loads.
                Box(Modifier.fillMaxWidth().padding(vertical = 20.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                LaunchedEffect(next) { onLoadMore(next) }
            }
        } else if (list.moreFailed) {
            item(key = "more-failed") {
                AttentionBanner(
                    icon = R.drawable.ic_cloud_off,
                    text = stringResource(R.string.my_trips_error_title),
                    modifier = Modifier.padding(top = 16.dp),
                    action = stringResource(R.string.my_trips_retry),
                    onAction = onRetryMore,
                )
            }
        }
    }
}

/** A refresh that failed, over the list it left as it was. */
@Composable
private fun RefreshFailed(onRefresh: () -> Unit) {
    AttentionBanner(
        icon = R.drawable.ic_cloud_off,
        text = stringResource(R.string.my_trips_error_title),
        modifier = Modifier.padding(top = 8.dp),
        action = stringResource(R.string.my_trips_retry),
        onAction = onRefresh,
    )
}

/** As the runs' days are written: the weekday, month and day, in the phone's language, and the year when it differs. */
private fun dateTitle(date: LocalDate, withYear: Boolean, locale: Locale): String =
    date.format(DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, if (withYear) "EEEEMMMMdy" else "EEEEMMMMd"), locale))

/** The route's badge, where the trip went, and when and on which vehicle; TalkBack reads it as one. */
@Composable
private fun TripRow(trip: MyTrip, zone: ZoneId, modifier: Modifier) {
    val locale = LocalConfiguration.current.locales[0]
    val row = remember(trip, zone, locale) { myTripRow(trip, zone, locale) }
    val detail = stringResource(R.string.my_trips_times, row.start, row.end, row.vehicle)
    val said = listOfNotNull(row.badge.ifEmpty { null }?.let { stringResource(R.string.setup_route, it) }, row.title, detail).joinToString()
    ListItem(
        leadingContent = { RouteBadge(row.badge, row.color, row.textColor) },
        headlineContent = { Text(row.title ?: detail) },
        supportingContent = row.title?.let { { Text(detail) } },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
        modifier = modifier.clearAndSetSemantics { contentDescription = said },
    )
}
