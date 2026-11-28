package org.onebusaway.vehicletracker.ui

import android.view.KeyEvent
import androidx.compose.foundation.text.input.TextFieldState
import androidx.compose.foundation.text.input.setTextAndPlaceCursorAtEnd
import androidx.compose.material3.Surface
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.PasswordChangeFailure
import org.onebusaway.vehicletracker.ui.settings.ChangePasswordContent
import org.onebusaway.vehicletracker.ui.settings.NewPasswordProblem
import org.onebusaway.vehicletracker.ui.settings.PasswordChangeForm
import org.onebusaway.vehicletracker.ui.settings.PasswordField
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Change password as the driver sees it: the button, the line under each field, the banner, and the wait. */
@RunWith(AndroidJUnit4::class)
class ChangePasswordScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)

    private val current = TextFieldState()
    private val new = TextFieldState()
    private val again = TextFieldState()
    private var form by mutableStateOf(PasswordChangeForm())
    private var changes = 0
    private var backs = 0
    private var leftNew = 0
    private val edited = mutableListOf<PasswordField>()

    private fun show(server: String? = "metro.example") = compose.setContent {
        AppTheme(darkTheme = false) {
            Surface {
                ChangePasswordContent(form, server, current, new, again, onBack = { backs++ }, onChange = { changes++ }, onEdited = { edited += it }, onLeftNew = { leftNew++ })
            }
        }
    }

    private fun button(id: Int) = compose.onNode(hasText(string(id)) and hasClickAction())

    private fun fields() = compose.onAllNodes(hasSetTextAction())

    @Test
    fun theButtonWaitsForAllThreeFields_thenChanges() {
        show()
        compose.onNodeWithText(string(R.string.change_password_rule)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.change_password_others)).assertIsDisplayed()
        button(R.string.change_password_title).assertIsNotEnabled()

        current.setTextAndPlaceCursorAtEnd("old-secret")
        new.setTextAndPlaceCursorAtEnd("new-secret")
        compose.waitForIdle()
        button(R.string.change_password_title).assertIsNotEnabled()
        again.setTextAndPlaceCursorAtEnd("new-secret")

        button(R.string.change_password_title).assertIsEnabled().performClick()
        assertEquals(1, changes)
    }

    @Test
    fun eachLineShowsUnderItsField_andTalkBackReadsItAsThatFieldsError() {
        form = PasswordChangeForm(newProblem = NewPasswordProblem.TOO_SHORT, noMatch = true, wrongCurrent = true)
        show()

        for (line in listOf(R.string.change_password_wrong_current, R.string.change_password_too_short, R.string.change_password_no_match)) {
            compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.Error, string(line))).assertExists()
        }
        compose.onAllNodesWithText(string(R.string.change_password_rule)).assertCountEquals(0)

        form = PasswordChangeForm(newProblem = NewPasswordProblem.TOO_LONG)
        compose.onNode(SemanticsMatcher.expectValue(SemanticsProperties.Error, string(R.string.change_password_too_long))).assertExists()
    }

    @Test
    fun theBannerSaysWhatNoFieldIsToBlameFor() {
        form = PasswordChangeForm(banner = PasswordChangeFailure.UNREACHABLE)
        show()
        compose.onNode(hasText("metro.example", substring = true) and hasText("connection", substring = true)).assertIsDisplayed()

        for ((failure, said) in listOf(
            PasswordChangeFailure.TOO_MANY_TRIES to R.string.login_error_too_many_tries,
            PasswordChangeFailure.OLDER_SERVER to R.string.change_password_older_server,
            PasswordChangeFailure.FAILED to R.string.change_password_failed,
        )) {
            form = PasswordChangeForm(banner = failure)
            compose.onNodeWithText(string(said)).assertIsDisplayed()
        }
    }

    @Test
    fun whileChanging_theButtonSaysSo_andTheFieldsAndBackWait() {
        current.setTextAndPlaceCursorAtEnd("old-secret")
        new.setTextAndPlaceCursorAtEnd("new-secret")
        again.setTextAndPlaceCursorAtEnd("new-secret")
        form = PasswordChangeForm(changing = true)
        show()

        compose.onNodeWithText(string(R.string.change_password_changing)).assertIsDisplayed()
        fields().fetchSemanticsNodes().forEachIndexed { i, _ -> fields()[i].assertIsNotEnabled() }
        compose.onNodeWithText(string(R.string.change_password_changing)).performClick()
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        compose.waitForIdle()

        assertEquals(0, changes)
        assertEquals("Back waits", 0, backs)
        assertTrue("the page is still there", compose.onAllNodesWithText(string(R.string.change_password_changing)).fetchSemanticsNodes().isNotEmpty())
    }

    @Test
    fun typingSaysWhichField_andLeavingNewPasswordChecksIt() {
        show()
        fields()[0].performClick()
        fields()[0].performTextInput("o")
        fields()[1].performClick()
        fields()[1].performTextInput("short")
        compose.waitForIdle()
        assertEquals(0, leftNew)

        fields()[2].performClick()
        compose.waitForIdle()

        assertEquals(1, leftNew)
        assertEquals(listOf(PasswordField.CURRENT, PasswordField.NEW), edited.distinct())
    }
}
