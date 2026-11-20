package org.onebusaway.vehicletracker.ui.help

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
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.ui.account.appVersion
import org.onebusaway.vehicletracker.ui.login.serverLabel
import java.time.ZoneId
import javax.inject.Inject

/** The About rows' values; empty until read, which is off the main thread and takes a moment. */
internal data class HelpAbout(val version: String = "", val server: String = "")

/** Help's facts about the app, and the text Share diagnostics sends. */
@HiltViewModel
class HelpViewModel @Inject constructor(
    @param:ApplicationContext private val context: Context,
    private val sessionStore: SessionStore,
) : ViewModel() {
    internal val about: StateFlow<HelpAbout> = flow {
        emit(HelpAbout(appVersion(context), sessionStore.session.first().serverUrl?.let(::serverLabel).orEmpty()))
    }.flowOn(Dispatchers.IO).stateIn(viewModelScope, SharingStarted.Eagerly, HelpAbout())

    /** The text for the share sheet, read and built off the main thread. */
    internal suspend fun diagnostics(): String = withContext(Dispatchers.IO) {
        diagnosticsText(deviceFacts(context), sessionStore.session.first(), AppLog.recent.snapshot(), ZoneId.systemDefault())
    }
}
