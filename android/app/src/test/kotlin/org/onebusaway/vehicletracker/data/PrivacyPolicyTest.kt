package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.BoundApi

/** [TrackerPrivacyPolicy] reading `GET /api/v1/agency` through the app's real API client. */
class PrivacyPolicyTest {
    private lateinit var server: MockWebServer
    private lateinit var policy: TrackerPrivacyPolicy

    @Before fun setUp() {
        server = MockWebServer().apply { start() }
        val url = server.url("/").toString()
        policy = TrackerPrivacyPolicy { BoundApi(url, ApiFactory { "jwt" }.create(url)) }
    }

    @After fun tearDown() = server.shutdown()

    private fun answer(body: String) = server.enqueue(MockResponse().setBody(body))

    @Test fun `the agency's https link is the one to open`() = runTest {
        answer("""{"privacy_policy_url":"https://metro.example/privacy"}""")

        assertEquals("https://metro.example/privacy", policy.link())

        val request = server.takeRequest()
        assertEquals("GET", request.method)
        assertEquals("/api/v1/agency", request.path)
        assertEquals("Bearer jwt", request.getHeader("Authorization"))
    }

    @Test fun `none set is no link`() = runTest {
        answer("""{"privacy_policy_url":null}""")

        assertNull(policy.link())
    }

    // A server from before GET /api/v1/agency existed.
    @Test fun `404 is no link`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404))

        assertNull(policy.link())
    }

    @Test fun `a link with its scheme in capitals opens with it in lower case`() = runTest {
        answer("""{"privacy_policy_url":"HTTPS://metro.example/Privacy"}""")

        assertEquals("https://metro.example/Privacy", policy.link())
    }

    @Test fun `a link that is not https is not opened`() = runTest {
        answer("""{"privacy_policy_url":"http://metro.example/privacy"}""")

        assertNull(policy.link())
    }

    @Test fun `a server error or no connection is no link this time, not a crash`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))
        assertNull(policy.link())

        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.DISCONNECT_AT_START))
        assertNull(policy.link())

        answer("not json")
        assertNull(policy.link())
    }

    @Test fun `with nobody signed in, nobody is asked`() = runTest {
        assertNull(TrackerPrivacyPolicy { null }.link())
        assertEquals(0, server.requestCount)
    }

    @Test fun `only an https address with a host opens, as it is but for its scheme`() {
        assertEquals("https://metro.example/privacy", httpsLink("https://metro.example/privacy"))
        assertEquals("https://metro.example", httpsLink("HTTPS://metro.example"))
        assertEquals("https://Metro.example/A?b=C", httpsLink("Https://Metro.example/A?b=C"))
        assertNull(httpsLink("http://metro.example/privacy"))
        assertNull(httpsLink("https:///privacy"))
        assertNull(httpsLink("javascript:alert(1)"))
        assertNull(httpsLink("https://metro example"))
        assertNull(httpsLink(""))
    }
}
