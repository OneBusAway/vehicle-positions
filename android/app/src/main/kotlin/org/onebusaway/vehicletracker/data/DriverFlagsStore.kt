package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.first
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.io.IOException
import java.security.MessageDigest
import java.util.Locale

/**
 * Something a driver is shown once on this phone, under the name it is [storedAs]. Each keeps its own set of
 * drivers, so a new one is a new name here and nothing else changes.
 */
@JvmInline
value class DriverFlag(val storedAs: String) {
    companion object {
        /** The driver has been through the welcome, or skipped it. */
        val WelcomeSeen = DriverFlag("welcome_seen")

        /** The run board's tip has been closed, or the driver tapped a run before it showed. */
        val RunBoardTipSeen = DriverFlag("run_board_tip_seen")

        /** The tip on Tracking's sent count has been closed, or the driver's first trip moved off or ended without it. */
        val SentCountTipSeen = DriverFlag("sent_count_tip_seen")

        /** The sheet after the driver's first End trip has shown. */
        val FirstTripDoneSeen = DriverFlag("first_trip_done_seen")
    }
}

/**
 * Who a [DriverFlag] is set for: the server's host and the email, lower-cased, as a SHA-256 [hash]. A hash, because
 * the flags outlive Sign out and a driver's email should not stay on a phone they have handed on. Per driver rather
 * than per phone, so the second driver on a shared phone is shown what the first one was.
 */
@JvmInline
value class DriverKey private constructor(val hash: String) {
    companion object {
        fun of(serverUrl: String, email: String): DriverKey {
            val host = serverUrl.toHttpUrlOrNull()?.host ?: serverUrl.trim()
            val driver = "$host|${email.trim()}".lowercase(Locale.ROOT)
            val digest = MessageDigest.getInstance("SHA-256").digest(driver.toByteArray())
            return DriverKey(digest.joinToString("") { "%02x".format(Locale.ROOT, it) })
        }

        /** Null for a session that names no driver: nobody signed in, or one an older build stored without the email. */
        fun of(session: Session): DriverKey? {
            val serverUrl = session.serverUrl ?: return null
            val email = session.email ?: return null
            return of(serverUrl, email)
        }
    }
}

/** What each driver has already been shown on this phone. Never synced to the server, and kept through Sign out. */
interface DriverFlagsStore {
    /** Throws [IOException] when the phone can't read its record. */
    suspend fun isSet(driver: DriverKey, flag: DriverFlag): Boolean

    /** Throws [IOException] when the phone can't save it. */
    suspend fun set(driver: DriverKey, flag: DriverFlag)
}

/** Internal, not private, only because [org.onebusaway.vehicletracker.di.AppModule] provides the store. */
internal val Context.driverFlagsDataStore: DataStore<Preferences> by preferencesDataStore(name = "driver_flags")

class DataStoreDriverFlagsStore(private val dataStore: DataStore<Preferences>) : DriverFlagsStore {
    override suspend fun isSet(driver: DriverKey, flag: DriverFlag): Boolean =
        driver.hash in dataStore.data.first()[driversWith(flag)].orEmpty()

    override suspend fun set(driver: DriverKey, flag: DriverFlag) {
        dataStore.edit { prefs -> prefs[driversWith(flag)] = prefs[driversWith(flag)].orEmpty() + driver.hash }
    }

    private fun driversWith(flag: DriverFlag) = stringSetPreferencesKey(flag.storedAs)
}
