package org.onebusaway.vehicletracker.data

import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.LoginRequest
import java.net.UnknownServiceException

/**
 * Covers what the JVM cannot: OkHttp refusing a plain http address under the device's real network
 * security policy. This fails if OkHttp ever changes what it throws, which a fake could not show.
 */
@RunWith(AndroidJUnit4::class)
class CleartextPolicyTest {
    @Test
    fun theNetworkPolicysRefusalReadsAsHttpsRequired() {
        // An IP literal outside the debug build's allowed hosts, so this needs no DNS and no network.
        val api = ApiFactory { null }.create("http://192.0.2.1:8080/")

        val refusal = assertThrows(UnknownServiceException::class.java) {
            runBlocking { api.login(LoginRequest("d@example.com", "pw")) }
        }

        assertEquals(ApiError.CleartextBlocked, mapLoginError(refusal))
    }
}
