package org.onebusaway.vehiclepositions.util

import android.content.Context
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKeys
import dagger.hilt.android.qualifiers.ApplicationContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * TokenManager
 * ─────────────────────────────────────────────────────────────────────────────
 * Secure storage for JWT access token and refresh token using
 * EncryptedSharedPreferences backed by Android Keystore (AES-256).
 *
 * IMPORTANT: This class is a storage facade only.
 * It never seeds or generates tokens.
 * The login flow (Milestone 2) is solely responsible for writing the
 * initial token via saveToken() after a successful /auth/login response.
 */
@Singleton
class TokenManager @Inject constructor(
    @ApplicationContext private val context: Context
) {
    private val masterKeyAlias = MasterKeys.getOrCreate(MasterKeys.AES256_GCM_SPEC)

    private val prefs = EncryptedSharedPreferences.create(
        "secure_prefs",
        masterKeyAlias,
        context,
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
    )

    companion object {
        private const val KEY_JWT           = "jwt_token"
        private const val KEY_REFRESH_TOKEN = "refresh_token"
        private const val TAG               = "TokenManager"
    }

    fun saveToken(token: String) {
        prefs.edit().putString(KEY_JWT, token).apply()
        // Never log the token value — it is a security credential
        android.util.Log.d(TAG, "Access token saved")
    }

    fun getToken(): String? = prefs.getString(KEY_JWT, null)

    fun saveRefreshToken(token: String) {
        prefs.edit().putString(KEY_REFRESH_TOKEN, token).apply()
        android.util.Log.d(TAG, "Refresh token saved")
    }

    fun getRefreshToken(): String? = prefs.getString(KEY_REFRESH_TOKEN, null)

    fun clearTokens() {
        prefs.edit()
            .remove(KEY_JWT)
            .remove(KEY_REFRESH_TOKEN)
            .apply()
        android.util.Log.d(TAG, "JWT and refresh token cleared from device")
    }

    fun isLoggedIn(): Boolean = getToken() != null
}