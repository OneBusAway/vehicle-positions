package org.onebusaway.vehicletracker.data.map

import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import org.onebusaway.vehicletracker.data.Session

/**
 * Calls [refresh] when collection starts with the driver signed in, each time they sign in, and
 * once a day after that for as long as collection lasts: a driver can keep the app in front for a
 * whole day and more. Calling it costs nothing while the map is current, because
 * [MapRepository.refresh] goes to the network only when a check is due.
 */
internal suspend fun refreshWhenSignedIn(session: Flow<Session>, refresh: () -> Unit) {
    session.map { it.token != null }.distinctUntilChanged().collectLatest { signedIn ->
        while (signedIn) {
            refresh()
            delay(CHECK_EVERY_S * 1000)
        }
    }
}
