package org.onebusaway.vehicletracker.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

data class Session(val serverUrl: String?, val token: String?, val issuedAtEpochSec: Long?) {
    fun hasFreshToken(nowEpochSec: Long): Boolean =
        token != null && issuedAtEpochSec != null && nowEpochSec - issuedAtEpochSec < 24 * 3600
}

interface SessionStore {
    val session: Flow<Session>
    suspend fun saveLogin(serverUrl: String, token: String, issuedAtEpochSec: Long)
    suspend fun clearToken()
}

private val Context.sessionDataStore by preferencesDataStore(name = "session")

class DataStoreSessionStore(private val context: Context) : SessionStore {
    private object Keys {
        val SERVER_URL = stringPreferencesKey("server_url")
        val TOKEN = stringPreferencesKey("token")
        val TOKEN_ISSUED_AT = longPreferencesKey("token_issued_at")
    }

    override val session: Flow<Session> = context.sessionDataStore.data.map { prefs ->
        Session(
            serverUrl = prefs[Keys.SERVER_URL],
            token = prefs[Keys.TOKEN],
            issuedAtEpochSec = prefs[Keys.TOKEN_ISSUED_AT],
        )
    }

    override suspend fun saveLogin(serverUrl: String, token: String, issuedAtEpochSec: Long) {
        context.sessionDataStore.edit { prefs ->
            prefs[Keys.SERVER_URL] = serverUrl
            prefs[Keys.TOKEN] = token
            prefs[Keys.TOKEN_ISSUED_AT] = issuedAtEpochSec
        }
    }

    override suspend fun clearToken() {
        context.sessionDataStore.edit { prefs ->
            prefs.remove(Keys.TOKEN)
            prefs.remove(Keys.TOKEN_ISSUED_AT)
        }
    }
}
