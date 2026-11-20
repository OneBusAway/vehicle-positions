package org.onebusaway.vehicletracker.ui

import android.os.Build
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher.Companion.expectValue
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.language.LanguageList
import org.onebusaway.vehicletracker.ui.language.LanguageRow
import org.onebusaway.vehicletracker.ui.language.LanguageRowContent
import org.onebusaway.vehicletracker.ui.language.appLanguage
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import java.util.Locale

/** The Language row and its list: what they say, what TalkBack reads, and what a tap does. */
@RunWith(AndroidJUnit4::class)
class LanguageRowTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext

    private fun show(content: @Composable () -> Unit) {
        compose.setContent { AppTheme(darkTheme = false) { Surface { content() } } }
    }

    private val english = appLanguage(Locale.ENGLISH)
    private val hindi = appLanguage(Locale.forLanguageTag("hi"))
    private val spanish = appLanguage(Locale.forLanguageTag("es"))

    @Test
    fun theRowNamesTheLanguageShown_andOnAndroid13OpensTheAppsList() {
        assumeTrue("the app's own list comes with Android 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        show { LanguageRow() }

        val row = compose.onNode(hasText(context.getString(R.string.language_row, "English")) and hasClickAction())
        row.assert(expectValue(SemanticsProperties.Role, Role.Button))
        compose.onNode(hasText(context.getString(R.string.language_row_phone_note))).assertDoesNotExist()
        row.performClick()

        compose.onNode(hasText(context.getString(R.string.language_list_title))).assertExists()
        compose.onNode(hasText("English") and hasClickAction()).assertIsSelected()
        compose.onNode(hasText(context.getString(R.string.language_list_cancel))).performClick()
        compose.onNode(hasText(context.getString(R.string.language_list_title))).assertDoesNotExist()
    }

    @Test
    fun belowAndroid13_theRowSaysTheLanguageIsThePhones() {
        var taps = 0
        show { LanguageRowContent(english, opensPhoneSettings = true, enabled = true) { taps++ } }

        val words = hasText(context.getString(R.string.language_row, "English")) and
            hasText(context.getString(R.string.language_row_phone_note))
        compose.onNode(words and hasClickAction()).performClick()

        assertEquals(1, taps)
    }

    @Test
    fun theListNamesEachLanguageInItsOwnScript_withItsEnglishNameUnder() {
        var chosen: String? = null
        show { LanguageList(listOf(english, hindi, spanish), shownTag = "en", onChoose = { chosen = it }, onDismiss = {}) }

        // Each is one radio button, read as its own name then its English one; English is named once.
        compose.onNode(hasText("English") and hasClickAction()).assertIsSelected()
            .assert(expectValue(SemanticsProperties.Role, Role.RadioButton))
        compose.onNode(hasText(hindi.ownName) and hasText("Hindi") and hasClickAction()).assertIsNotSelected()
        compose.onNode(hasText("Español") and hasText("Spanish") and hasClickAction()).assertIsNotSelected()

        compose.onNode(hasText(hindi.ownName) and hasClickAction()).performClick()

        assertEquals("hi", chosen)
    }

    @Test
    fun anAppWithOneLanguage_listsThatOne() {
        show { LanguageList(listOf(english), shownTag = "en", onChoose = {}, onDismiss = {}) }

        compose.onNode(hasText("English") and hasClickAction()).assertIsSelected()
        compose.onNode(hasText(context.getString(R.string.language_list_cancel)) and hasClickAction()).assertExists()
    }
}
