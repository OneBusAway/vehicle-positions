package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.TrackingProblem
import org.onebusaway.vehicletracker.data.TrackingState
import org.onebusaway.vehicletracker.ui.login.LoginScreenContent
import org.onebusaway.vehicletracker.ui.login.LoginUiState
import org.onebusaway.vehicletracker.ui.tracking.TrackingScreenContent
import org.onebusaway.vehicletracker.ui.tracking.TrackingUiState

/**
 * Instrumented UI tests that render the stateless screen `Content` composables directly with
 * fake state and no-op callbacks — no Hilt/ViewModel wiring needed. Run with
 * `./gradlew :app:connectedDebugAndroidTest`.
 */
@RunWith(AndroidJUnit4::class)
class ScreenFlowTest {
    @get:Rule val compose = createComposeRule()

    private fun getString(resId: Int): String =
        InstrumentationRegistry.getInstrumentation().targetContext.getString(resId)

    @Test
    fun loginScreen_disablesButtonWhileLoading() {
        compose.setContent {
            LoginScreenContent(
                state = LoginUiState(loading = true),
                onServerUrlChange = {},
                onEmailChange = {},
                onPasswordChange = {},
                onLoginClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.login_button)).assertIsNotEnabled()
    }

    @Test
    fun trackingScreen_showsConnectedStatus() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(
                    tracking = TrackingState(active = true, problem = TrackingProblem.NONE),
                ),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.tracking_status_connected)).assertIsDisplayed()
    }

    @Test
    fun trackingScreen_endTripShowsConfirmation() {
        compose.setContent {
            TrackingScreenContent(
                state = TrackingUiState(
                    tracking = TrackingState(active = true, problem = TrackingProblem.NONE),
                ),
                onEndTripClick = {},
                onEndTripLocallyClick = {},
                onDismissError = {},
                onReauthClick = {},
            )
        }

        compose.onNodeWithText(getString(R.string.tracking_end_trip_button)).performClick()

        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_title)).assertIsDisplayed()
        compose.onNodeWithText(getString(R.string.tracking_end_trip_dialog_message)).assertIsDisplayed()
    }
}
