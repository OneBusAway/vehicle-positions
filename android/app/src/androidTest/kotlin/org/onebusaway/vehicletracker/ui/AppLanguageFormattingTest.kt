package org.onebusaway.vehicletracker.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.test.DeviceConfigurationOverride
import androidx.compose.ui.test.Locales
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.text.intl.LocaleList
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.data.ActiveTrip
import org.onebusaway.vehicletracker.ui.resume.ResumeCard
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftScreenContent
import org.onebusaway.vehicletracker.ui.resume.ResumeShiftUiState
import org.onebusaway.vehicletracker.ui.routes.RouteBadgeModel
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.Locale

/**
 * Times are written in the app's language as Compose has it, which can differ from the phone's default: a language
 * chosen for the app alone, or a screen being rebuilt for a new one.
 */
@RunWith(AndroidJUnit4::class)
class AppLanguageFormattingTest {
    @get:Rule val compose = createComposeRule()

    private val pacific = ZoneId.of("America/Los_Angeles")
    private val runAt = ZonedDateTime.of(2026, 9, 2, 18, 58, 0, 0, pacific).toInstant()
    private val card = ResumeCard(
        route = RouteBadgeModel("7", Color(0xFFFDB71A), Color.Black),
        headsign = "Downtown Seattle",
        runStartsAt = runAt,
        vehicleId = "4317",
        startedAt = runAt,
        zone = pacific,
    )

    @Test
    fun theResumeCardWritesItsTimesInTheAppsLanguage() {
        // A default that writes 6:58 PM, whatever the test phone is set to.
        val default = Locale.getDefault()
        Locale.setDefault(Locale.US)
        try {
            compose.setContent {
                DeviceConfigurationOverride(DeviceConfigurationOverride.Locales(LocaleList("de"))) {
                    ResumeShiftScreenContent(
                        ResumeShiftUiState(trip = ActiveTrip(7L, "T1", "4317", "100263", "20260902", 100L), card = card),
                        { runAt.epochSecond + 23 * 60 },
                        {}, {}, {}, {},
                    )
                }
            }

            // The run's start on German's 24-hour clock (the words stay English: there is no German yet), and no time
            // on the default's 12-hour one.
            compose.onNodeWithText("18:58 run").assertExists()
            assertTrue(compose.onAllNodesWithText("PM", substring = true).fetchSemanticsNodes().isEmpty())
        } finally {
            Locale.setDefault(default)
        }
    }
}
