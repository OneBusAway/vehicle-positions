package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.flow.map
import org.onebusaway.vehicletracker.data.Session

/**
 * Calls [refresh] when collection starts with the driver signed in, and again each time they sign
 * in. Calling it on every resume costs nothing once the map is current: [MapRepository.refresh]
 * asks the network only when a check is due.
 */
internal suspend fun refreshWhenSignedIn(session: Flow<Session>, refresh: () -> Unit) {
    session.map { it.token != null }.distinctUntilChanged().filter { it }.collect { refresh() }
}
