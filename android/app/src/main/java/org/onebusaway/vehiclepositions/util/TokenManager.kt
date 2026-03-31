package org.onebusaway.vehiclepositions.util

import android.content.Context
import android.content.SharedPreferences
import android.util.Log
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Secure storage for JWT and refresh tokens.
 * IMPORTANT: This class is a storage facade only.
 * The login flow (Milestone 2) is solely responsible for writing the
 * initial token via saveToken() after a successful /auth/login response.
 */
@Singleton
class TokenManager @Inject constructor(
    @ApplicationContext private val context: Context
) {
    companion object {
        private const val TAG = "TokenManager"
        private const val KEY_JWT = "jwt_token"
        private const val KEY_REFRESH_TOKEN = "refresh_token"
        private const val PREFS_FILE = "secure_prefs"
    }

    // lazy init with keystore recovery fallback
    // MasterKey.Builder and EncryptedSharedPreferences can throw on
    // corrupted keystore - a well-documented issue on many devices
    private val prefs: SharedPreferences by lazy {
        try {
            val masterKey = MasterKey.Builder(context)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()
            EncryptedSharedPreferences.create(
                context,
                PREFS_FILE,
                masterKey,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
            )
        } catch (e: Exception) {
            // Keystore corrupted - clear and retry once
            Log.e(TAG, "Keystore error — attempting recovery: ${e.message}")
            context.deleteSharedPreferences(PREFS_FILE)
            val masterKey = MasterKey.Builder(context)
                .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
                .build()
            EncryptedSharedPreferences.create(
                context,
                PREFS_FILE,
                masterKey,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
            )
        }
    }

    fun saveToken(token: String) {
        prefs.edit().putString(KEY_JWT, token).apply()
    }

    fun getToken(): String? = prefs.getString(KEY_JWT, null)

    fun saveRefreshToken(token: String) {
        prefs.edit().putString(KEY_REFRESH_TOKEN, token).apply()
    }

    fun getRefreshToken(): String? = prefs.getString(KEY_REFRESH_TOKEN, null)

    fun clearTokens() {
        prefs.edit()
            .remove(KEY_JWT)
            .remove(KEY_REFRESH_TOKEN)
            .apply()
        Log.d(TAG, "JWT cleared from device")
    }

    fun isLoggedIn(): Boolean = getToken() != null
}