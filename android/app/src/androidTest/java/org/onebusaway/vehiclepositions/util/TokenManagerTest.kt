package org.onebusaway.vehiclepositions.util

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class TokenManagerTest {

    private lateinit var context: Context
    private lateinit var tokenManager: TokenManager

    @Before
    fun setup() {
        context = ApplicationProvider.getApplicationContext()
        tokenManager = TokenManager(context)
        tokenManager.clearTokens()
    }

    @Test
    fun initialTokenIsNull() {
        assertNull(tokenManager.getToken())
    }

    @Test
    fun initialRefreshTokenIsNull() {
        assertNull(tokenManager.getRefreshToken())
    }

    @Test
    fun isLoggedInReturnsFalseWhenNoToken() {
        assertFalse(tokenManager.isLoggedIn())
    }

    @Test
    fun saveTokenStoresTokenCorrectly() {
        tokenManager.saveToken("test_jwt_token")
        assertEquals("test_jwt_token", tokenManager.getToken())
    }

    @Test
    fun saveRefreshTokenStoresRefreshTokenCorrectly() {
        tokenManager.saveRefreshToken("test_refresh_token")
        assertEquals("test_refresh_token", tokenManager.getRefreshToken())
    }

    @Test
    fun isLoggedInReturnsTrueAfterSaveToken() {
        tokenManager.saveToken("test_jwt_token")
        assertTrue(tokenManager.isLoggedIn())
    }

    @Test
    fun clearTokensRemovesBothTokens() {
        tokenManager.saveToken("test_jwt_token")
        tokenManager.saveRefreshToken("test_refresh_token")
        tokenManager.clearTokens()
        assertNull(tokenManager.getToken())
        assertNull(tokenManager.getRefreshToken())
    }

    @Test
    fun isLoggedInReturnsFalseAfterClearTokens() {
        tokenManager.saveToken("test_jwt_token")
        tokenManager.clearTokens()
        assertFalse(tokenManager.isLoggedIn())
    }
}