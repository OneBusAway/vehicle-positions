package org.onebusaway.vehicletracker.ui.settings

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.withContext
import org.onebusaway.vehicletracker.data.AppLog
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.WaitingReports
import org.onebusaway.vehicletracker.ui.account.appVersion
import org.onebusaway.vehicletracker.ui.help.deviceFacts
import org.onebusaway.vehicletracker.ui.help.diagnosticsText
import java.time.ZoneId
import javax.inject.Inject

/** What Settings shows besides who is signed in, and the text Share diagnostics sends. */
@HiltViewModel
class SettingsViewModel @Inject constructor(
    @param:ApplicationContext private val context: Context,
    private val sessionStore: SessionStore,
    waitingReports: WaitingReports,
) : ViewModel() {
    /** Empty until read, which is off the main thread. */
    internal val version: StateFlow<String> =
        flow { emit(appVersion(context)) }.flowOn(Dispatchers.IO).stateIn(viewModelScope, SharingStarted.Eagerly, "")

    internal val waiting: StateFlow<ReportsWaiting?> =
        waitingReports.waiting.stateIn(viewModelScope, SharingStarted.Eagerly, null)

    /** The text for the share sheet, read and built off the main thread. */
    internal suspend fun diagnostics(): String = withContext(Dispatchers.IO) {
        diagnosticsText(deviceFacts(context), sessionStore.session.first(), AppLog.recent.snapshot(), ZoneId.systemDefault())
    }
}
