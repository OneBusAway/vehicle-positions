package org.onebusaway.vehicletracker.ui

import androidx.lifecycle.SavedStateHandle
import org.junit.Assert.assertEquals
import org.junit.Test
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.PermissionAnswers
import org.onebusaway.vehicletracker.ui.permissions.PermissionRow
import org.onebusaway.vehicletracker.ui.permissions.afterAsking
import org.onebusaway.vehicletracker.ui.permissions.fixOf

/** What a tap on a row of Permission check does, how an answer from Android changes it, and where that is kept. */
class PermissionFixesTest {
    private val none = emptySet<PermissionRow>()

    @Test
    fun `Android is asked about every row but location services`() {
        for (row in PermissionRow.entries) {
            assertEquals("$row", if (row == PermissionRow.LOCATION_SERVICES) Fix.TURN_ON else Fix.ALLOW, fixOf(row, none))
        }
    }

    @Test
    fun `a row only the settings can change opens them, and location services never does`() {
        assertEquals(Fix.OPEN_SETTINGS, fixOf(PermissionRow.NOTIFICATIONS, setOf(PermissionRow.NOTIFICATIONS)))
        assertEquals(Fix.ALLOW, fixOf(PermissionRow.LOCATION, setOf(PermissionRow.NOTIFICATIONS)))
        assertEquals(Fix.TURN_ON, fixOf(PermissionRow.LOCATION_SERVICES, PermissionRow.entries.toSet()))
    }

    @Test
    fun `a first refusal can be asked about again`() {
        assertEquals(none, none.afterAsking(PermissionRow.LOCATION, allowed = false, rationale = true))
    }

    @Test
    fun `an ask Android would not show again leaves the settings, for that row alone`() {
        assertEquals(setOf(PermissionRow.LOCATION), none.afterAsking(PermissionRow.LOCATION, allowed = false, rationale = false))
        val notifications = setOf(PermissionRow.NOTIFICATIONS)
        assertEquals(notifications + PermissionRow.LOCATION, notifications.afterAsking(PermissionRow.LOCATION, allowed = false, rationale = false))
    }

    @Test
    fun `an allowed row can be asked about again should it be taken away`() {
        assertEquals(none, setOf(PermissionRow.LOCATION).afterAsking(PermissionRow.LOCATION, allowed = true, rationale = false))
    }

    @Test
    fun `nothing is only in the settings until Android has answered`() {
        assertEquals(none, PermissionAnswers(SavedStateHandle()).onlyInSettings.value)
    }

    @Test
    fun `a refusal for good is kept in the activity's saved state, for the answers read back from it`() {
        val saved = SavedStateHandle()
        PermissionAnswers(saved).answered(PermissionRow.LOCATION, allowed = false, rationale = false)

        assertEquals(setOf(PermissionRow.LOCATION), PermissionAnswers(saved).onlyInSettings.value)
    }

    @Test
    fun `each answer changes its own row`() {
        val answers = PermissionAnswers(SavedStateHandle())
        answers.answered(PermissionRow.LOCATION, allowed = false, rationale = false)
        answers.answered(PermissionRow.NOTIFICATIONS, allowed = false, rationale = false)

        answers.answered(PermissionRow.LOCATION, allowed = true, rationale = false)

        assertEquals(setOf(PermissionRow.NOTIFICATIONS), answers.onlyInSettings.value)
    }
}
