package org.onebusaway.vehicletracker.ui.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.PasswordChangeFailure

/** What Change password checks before anything is sent, and where each answer shows. */
class PasswordChangeFormTest {
    private fun begun(current: String = "old-secret", new: String = "new-secret", again: String = new) =
        PasswordChangeForm().begin(current, new, again)

    @Test fun `the button waits for all three fields`() {
        assertFalse(canChange("", "new-secret", "new-secret"))
        assertFalse(canChange("old-secret", "", "new-secret"))
        assertFalse(canChange("old-secret", "new-secret", ""))
        assertTrue(canChange("old-secret", "new-secret", "new-secret"))
        assertNull("nothing to say about an empty field: the button can't be tapped", begun(current = ""))
    }

    // As the server counts: code points for the 8, UTF-8 bytes for bcrypt's 72.
    @Test fun `the server's rules`() {
        assertEquals(NewPasswordProblem.TOO_SHORT, problemWith("1234567"))
        assertNull(problemWith("12345678"))
        assertEquals(NewPasswordProblem.TOO_SHORT, problemWith("\u00e9\u00e9\u00e9\u00e9\u00e9\u00e9\u00e9"))
        assertNull("four letters on screen, eight code points, as the server counts runes", problemWith("e\u0301e\u0301e\u0301e\u0301"))
        assertNull(problemWith("a".repeat(72)))
        assertEquals(NewPasswordProblem.TOO_LONG, problemWith("a".repeat(73)))
        assertEquals("8 UTF-16 units, but 4 code points as the server counts them", NewPasswordProblem.TOO_SHORT, problemWith("\uD83D\uDE00".repeat(4)))
        assertNull("18 code points, 72 bytes", problemWith("\uD83D\uDE00".repeat(18)))
        assertEquals("76 bytes", NewPasswordProblem.TOO_LONG, problemWith("\uD83D\uDE00".repeat(19)))
    }

    @Test fun `too short and no match show together, and nothing is sent`() {
        val form = begun(new = "short", again = "shorter")!!
        assertFalse(form.changing)
        assertEquals(NewPasswordProblem.TOO_SHORT, form.newProblem)
        assertTrue(form.noMatch)
        assertFalse(form.wrongCurrent)
    }

    @Test fun `a good change is under way, and a second tap sends nothing`() {
        val form = begun()!!
        assertTrue(form.changing)
        assertNull(form.begin("old-secret", "new-secret", "new-secret"))
    }

    @Test fun `a wrong current password shows under it, and the rest in the banner`() {
        val wrong = begun()!!.failed(PasswordChangeFailure.WRONG_CURRENT)
        assertTrue(wrong.wrongCurrent)
        assertNull(wrong.banner)
        assertFalse(wrong.changing)
        for (failure in PasswordChangeFailure.entries - PasswordChangeFailure.WRONG_CURRENT) {
            val form = begun()!!.failed(failure)
            assertEquals(failure, form.banner)
            assertFalse(form.wrongCurrent)
        }
    }

    @Test fun `typing in a field takes back only its own line`() {
        var form = begun(new = "short", again = "shorter")!!
        form = form.edited(PasswordField.AGAIN)
        assertFalse(form.noMatch)
        assertEquals(NewPasswordProblem.TOO_SHORT, form.newProblem)
        form = form.edited(PasswordField.CURRENT)
        assertEquals(NewPasswordProblem.TOO_SHORT, form.newProblem)
        form = form.edited(PasswordField.NEW)
        assertNull(form.newProblem)

        var wrong = begun()!!.failed(PasswordChangeFailure.WRONG_CURRENT)
        wrong = wrong.edited(PasswordField.NEW).edited(PasswordField.AGAIN)
        assertTrue(wrong.wrongCurrent)
        assertFalse(wrong.edited(PasswordField.CURRENT).wrongCurrent)
    }

    @Test fun `a new password typed again takes back the mismatch too`() {
        assertFalse(begun(again = "new-secrex")!!.edited(PasswordField.NEW).noMatch)
    }

    @Test fun `any typing takes back the banner`() {
        assertNull(begun()!!.failed(PasswordChangeFailure.UNREACHABLE).edited(PasswordField.AGAIN).banner)
    }

    @Test fun `leaving New password says what is wrong with it`() {
        assertNull("left empty, it is not wrong yet", PasswordChangeForm().leftNew("").newProblem)
        assertEquals(NewPasswordProblem.TOO_SHORT, PasswordChangeForm().leftNew("short").newProblem)
        assertNull(PasswordChangeForm().leftNew("short").leftNew("long enough").newProblem)
    }
}
