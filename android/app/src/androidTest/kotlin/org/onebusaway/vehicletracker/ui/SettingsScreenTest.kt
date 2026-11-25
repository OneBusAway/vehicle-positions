package org.onebusaway.vehicletracker.ui

import android.content.Intent
import android.os.Build
import android.view.KeyEvent
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.requiredHeight
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.click
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasScrollAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.isSelectable
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.requestFocus
import androidx.compose.ui.test.swipeUp
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.core.content.IntentCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.data.ReportsWaiting
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.data.WaitingOutlook
import org.onebusaway.vehicletracker.ui.account.AccountButton
import org.onebusaway.vehicletracker.ui.account.AccountUiState
import org.onebusaway.vehicletracker.ui.account.LocalOpenSettings
import org.onebusaway.vehicletracker.ui.permissions.Fix
import org.onebusaway.vehicletracker.ui.permissions.Grants
import org.onebusaway.vehicletracker.ui.settings.PhoneAccess
import org.onebusaway.vehicletracker.ui.settings.Readiness
import org.onebusaway.vehicletracker.ui.settings.SettingsContent
import org.onebusaway.vehicletracker.ui.settings.readiness
import org.onebusaway.vehicletracker.ui.settings.shareIntent
import org.onebusaway.vehicletracker.ui.theme.AppTheme
import org.onebusaway.vehicletracker.ui.theme.DarkColors
import org.onebusaway.vehicletracker.ui.theme.LightColors

/** Settings: what its header says, who is signed in and Sign out, its rows, how it folds, and the largest text. */
@RunWith(AndroidJUnit4::class)
class SettingsScreenTest {
    @get:Rule val compose = createComposeRule()

    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private fun string(id: Int, vararg args: Any) = context.getString(id, *args)
    private fun plural(id: Int, count: Int, vararg args: Any) = context.resources.getQuantityString(id, count, *args)

    private val everything = Grants(Build.VERSION_CODES.BAKLAVA, fine = true, coarse = true, background = true, notifications = true, batteryExempt = true)
    private val refused = everything.copy(fine = false, coarse = false, background = false)

    private fun ready(grants: Grants = everything, locationServicesOn: Boolean = true, waiting: ReportsWaiting? = null) =
        readiness(PhoneAccess(grants, locationServicesOn), waiting)

    private val account = AccountUiState(email = "d@example.com", server = "tracker.example.com")

    private class Calls {
        var back = 0
        var fixes = 0
        var permissionCheck = 0
        var signOuts = 0
        var signedOut = 0
        val themes = mutableListOf<ThemeChoice>()
        var help = 0
        var share = 0
    }

    private val calls = Calls()

    @Composable
    private fun Content(
        readiness: Readiness = ready(),
        locationFix: Fix = Fix.ALLOW,
        account: AccountUiState = this.account,
        theme: ThemeChoice = ThemeChoice.SYSTEM,
        dark: Boolean = false,
    ) {
        AppTheme(darkTheme = dark) {
            SettingsContent(
                readiness = readiness,
                locationFix = locationFix,
                account = account,
                version = "0.1.0 (1)",
                theme = theme,
                onBack = { calls.back++ },
                onFix = { calls.fixes++ },
                onPermissionCheck = { calls.permissionCheck++ },
                onSignOut = { calls.signOuts++ },
                onSignedOut = { calls.signedOut++ },
                onChooseTheme = { calls.themes += it },
                onHelp = { calls.help++ },
                onShare = { calls.share++ },
            )
        }
    }

    private fun says(title: Int, sentence: String) =
        compose.onNodeWithContentDescription(string(R.string.preflight_row_state, string(title), sentence))

    /** A change shows a moment late, so a driver back from the phone's settings sees it happen. */
    private fun letTheChangeShow() {
        compose.mainClock.advanceTimeBy(1_000)
        compose.waitForIdle()
    }

    private fun Dp.toPx(density: Density) = with(density) { toPx() }

    /** The button under the words, by what it says it does. */
    private fun fix(name: Int) = hasText(string(name)) and hasClickAction()

    private val fixNames = listOf(R.string.settings_allow_location, R.string.settings_turn_on_location, R.string.preflight_open_settings)

    // --- The header ---

    @Test
    fun theHeaderSaysEachState_andHasAButtonOnlyWhenATripCantStart() {
        val battery = string(R.string.preflight_battery)
        val states = listOf(
            Triple(ready(), R.string.settings_ready, string(R.string.settings_ready_body)),
            Triple(ready(everything.copy(batteryExempt = false)), R.string.settings_ready, plural(R.plurals.settings_fix, 1, 1, battery)),
            Triple(
                ready(everything.copy(batteryExempt = false, notifications = false, background = false)),
                R.string.settings_ready,
                plural(R.plurals.settings_fix_many, 3, 3),
            ),
            Triple(ready(waiting = ReportsWaiting(12, WaitingOutlook.WILL_SEND)), R.string.settings_ready, plural(R.plurals.tracking_waiting, 12, 12)),
            Triple(ready(waiting = ReportsWaiting(12, WaitingOutlook.NEVER)), R.string.settings_ready, plural(R.plurals.tracking_waiting_never, 12, 12)),
            Triple(ready(refused), R.string.settings_not_ready, string(R.string.settings_location_refused)),
            Triple(ready(locationServicesOn = false), R.string.settings_not_ready, string(R.string.settings_location_off)),
        )
        var shown by mutableIntStateOf(0)
        compose.setContent { Content(states[shown].first) }

        for ((index, state) in states.withIndex()) {
            shown = index
            letTheChangeShow()

            says(state.second, state.third).assertExists()
            val buttons = fixNames.sumOf { compose.onAllNodes(fix(it)).fetchSemanticsNodes().size }
            assertEquals("${state.third}: the button", if (state.first.canReport) 0 else 1, buttons)
        }
    }

    @Test
    fun twoThingsToFix_areBothNamed() {
        compose.setContent { Content(ready(everything.copy(batteryExempt = false, notifications = false))) }

        val notifications = string(R.string.preflight_notifications)
        val battery = string(R.string.preflight_battery)
        val both = SemanticsMatcher("names both") { node ->
            node.config.getOrElse(SemanticsProperties.ContentDescription) { emptyList() }.any { notifications in it && battery in it }
        }
        compose.onNode(both).assertExists()
    }

    @Test
    fun theButtonSaysWhatItWillDo() {
        val states = listOf(
            Triple(ready(refused), Fix.ALLOW, R.string.settings_allow_location),
            Triple(ready(refused), Fix.OPEN_SETTINGS, R.string.preflight_open_settings),
            Triple(ready(locationServicesOn = false), Fix.ALLOW, R.string.settings_turn_on_location),
        )
        var shown by mutableIntStateOf(0)
        compose.setContent { Content(states[shown].first, states[shown].second) }

        for ((index, state) in states.withIndex()) {
            shown = index
            letTheChangeShow()
            compose.onNode(fix(state.third)).performClick()
            assertEquals(index + 1, calls.fixes)
        }
        assertEquals("the button is not the header", 0, calls.permissionCheck)
    }

    @Test
    fun aTapOnTheHeaderOpensPermissionCheck() {
        compose.setContent { Content() }

        compose.onRoot().performTouchInput { click(Offset(centerX, 150.dp.toPx())) }
        assertEquals(1, calls.permissionCheck)

        val header = says(R.string.settings_ready, string(R.string.settings_ready_body))
        header.assert(SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Button))
        header.assert(SemanticsMatcher("its tap is named") { it.config[SemanticsActions.OnClick].label == string(R.string.permission_check_title) })
        header.performSemanticsAction(SemanticsActions.OnClick)
        assertEquals("as TalkBack taps it", 2, calls.permissionCheck)
    }

    @Test
    fun aTapOnTheFoldedBarOpensPermissionCheck() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content(ready(refused)) } }
        compose.onNode(hasText(string(R.string.help_version))).performScrollTo()
        compose.waitForIdle()
        val title = compose.onNode(hasText(string(R.string.settings_title)) and isHeading()).fetchSemanticsNode().boundsInRoot

        compose.onRoot().performTouchInput { click(Offset(width * 0.8f, title.center.y)) }

        assertEquals(1, calls.permissionCheck)
        assertEquals(0, calls.back)
    }

    @Test
    fun aTapOnTheBarWhereTheButtonHasGoneUnderIt_opensPermissionCheckInstead() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content(ready(refused)) } }
        val button = compose.onNode(fix(R.string.settings_allow_location))
        fun middle() = button.fetchSemanticsNode().let { it.positionInRoot.y + it.size.height / 2 }
        val bar = compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).fetchSemanticsNode().boundsInRoot.bottom
        val goneUnder = middle() - bar + 8.dp.toPx(compose.density)
        compose.onNode(hasScrollAction()).performSemanticsAction(SemanticsActions.ScrollBy) { it(0f, goneUnder) }
        compose.waitForIdle()
        val at = button.fetchSemanticsNode().positionInRoot.x + button.fetchSemanticsNode().size.width - 8.dp.toPx(compose.density)
        assertTrue("the button's middle, ${middle()}, is under the bar, which ends at $bar", middle() < bar)

        val tapped = Offset(at, middle())
        compose.onRoot().performTouchInput { click(tapped) }

        assertEquals(1, calls.permissionCheck)
        assertEquals(0, calls.fixes)
        assertEquals(0, calls.back)
    }

    @Test
    fun thePermissionCheckRowSaysHowThingsStand_andOpensIt() {
        val states = listOf(
            ready() to string(R.string.settings_permissions_all),
            ready(everything.copy(batteryExempt = false)) to plural(R.plurals.settings_fix_many, 1, 1),
            ready(everything.copy(batteryExempt = false, notifications = false)) to plural(R.plurals.settings_fix_many, 2, 2),
            ready(refused) to string(R.string.settings_permissions_refused),
            ready(locationServicesOn = false) to string(R.string.settings_permissions_off),
        )
        var shown by mutableIntStateOf(0)
        compose.setContent { Content(states[shown].first) }

        for ((index, state) in states.withIndex()) {
            shown = index
            letTheChangeShow()
            compose.onNode(hasText(string(R.string.permission_check_title)) and hasText(state.second) and hasClickAction()).performScrollTo().performClick()
            assertEquals(state.second, index + 1, calls.permissionCheck)
        }
    }

    @Test
    fun aChangeSeenWhileOpen_isSaidOnceItShows_andTheButtonGoes() {
        var state by mutableStateOf(ready(refused))
        compose.setContent { Content(state) }

        state = ready()
        letTheChangeShow()

        says(R.string.settings_ready, string(R.string.settings_ready_body)).assertExists()
        for (name in fixNames) compose.onNode(fix(name)).assertDoesNotExist()
    }

    @Test
    fun scrolled_theHeaderFoldsToItsBarAndStays() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content() } }
        val back = compose.onNodeWithContentDescription(string(R.string.setup_navigate_up))
        val title = compose.onNode(hasText(string(R.string.settings_title)) and isHeading())
        val backTop = back.fetchSemanticsNode().boundsInRoot.top
        val version = hasText(string(R.string.help_version))

        compose.onNode(version).performScrollTo()
        compose.waitForIdle()

        compose.onNode(version).assertIsDisplayed()
        title.assertIsDisplayed()
        back.assertIsDisplayed()
        assertEquals("Back stays where it was", backTop, back.fetchSemanticsNode().boundsInRoot.top, 1f)

        back.performClick()
        assertEquals("and works", 1, calls.back)
    }

    /** Where a row is, on the screen or off it. */
    private fun top(text: Int) = compose.onNodeWithText(string(text)).fetchSemanticsNode().positionInRoot.y

    @Test
    fun letGoInTheMiddleOfTheFold_theHeaderSettles() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content() } }
        val open = top(R.string.language_list_title)

        // A little of the fold, let go slowly, so it does not fling past it.
        compose.onNode(hasScrollAction()).performTouchInput { swipeUp(startY = centerY, endY = centerY - 40.dp.toPx(), durationMillis = 800) }
        compose.waitForIdle()

        assertEquals("back open", open, top(R.string.language_list_title), 1f)
    }

    @Test
    fun touchedWhileItSettles_thePageStaysUnderTheFingerThenSettles() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content() } }
        val page = compose.onNode(hasScrollAction())
        val open = top(R.string.language_list_title)

        page.performTouchInput {
            down(center)
            repeat(20) { moveBy(Offset(0f, -2.dp.toPx()), delayMillis = 40) }
            advanceEventTime(200)
        }
        val letGoAt = top(R.string.language_list_title)
        compose.mainClock.autoAdvance = false
        page.performTouchInput { up() }
        var settling = letGoAt
        var frames = 0
        while (settling <= letGoAt && frames++ < 60) {
            compose.mainClock.advanceTimeByFrame()
            settling = top(R.string.language_list_title)
        }
        assertTrue("settling: $settling, let go at $letGoAt, open $open", settling > letGoAt && settling < open)

        page.performTouchInput { down(center) }
        compose.mainClock.advanceTimeBy(200)
        val held = top(R.string.language_list_title)
        compose.mainClock.advanceTimeBy(1_000)
        assertTrue("held: $held, open $open", held < open)
        assertEquals("still under the finger", held, top(R.string.language_list_title), 0f)

        page.performTouchInput { up() }
        compose.mainClock.autoAdvance = true
        compose.waitForIdle()
        assertEquals("back open", open, top(R.string.language_list_title), 1f)
    }

    @Test
    fun aRowTalkBackScrollsTo_staysWhereItScrolledIt() {
        compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content() } }
        val open = top(R.string.language_list_title)

        compose.onNode(hasScrollAction()).performSemanticsAction(SemanticsActions.ScrollBy) { it(0f, 30f) }
        compose.waitForIdle()

        assertEquals(open - 30f, top(R.string.language_list_title), 1f)
    }

    @Test
    fun aRowKeyboardFocusMovesBackTo_comesOutFromUnderTheFoldedHeader() {
        InstrumentationRegistry.getInstrumentation().setInTouchMode(false)
        try {
            compose.setContent { Box(Modifier.requiredHeight(420.dp)) { Content() } }
            compose.onNode(hasText(string(R.string.help_version))).performScrollTo()
            compose.waitForIdle()

            compose.onNode(hasText(string(R.string.language_list_title)) and hasClickAction()).requestFocus()
            compose.waitForIdle()

            // Back is in the middle of the bar, which is 64 dp tall, and the header folds to 8 dp more.
            val back = compose.onNodeWithContentDescription(string(R.string.setup_navigate_up)).fetchSemanticsNode().boundsInRoot
            val folded = back.center.y + with(compose.density) { 40.dp.toPx() }
            assertTrue("${top(R.string.language_list_title)} is under the header, which ends at $folded", top(R.string.language_list_title) >= folded - 1f)
        } finally {
            InstrumentationRegistry.getInstrumentation().resetInTouchMode()
        }
    }

    // --- Who is signed in, and Sign out ---

    private fun askToSignOut() = compose.onNode(hasText(string(R.string.account_sign_out)) and hasClickAction()).performClick()

    private fun inDialog(text: Int) = compose.onNode(hasText(string(text)) and hasAnyAncestor(isDialog()))

    @Test
    fun theAccountIsReadAsTheEmailThenTheServer() {
        compose.setContent { Content() }

        val server = string(R.string.preflight_row_state, string(R.string.help_server), "tracker.example.com")
        compose.onNodeWithContentDescription("d@example.com, $server").assertIsDisplayed()
        compose.onNodeWithText(string(R.string.account_sign_out)).assertIsDisplayed()
    }

    @Test
    fun theAccountLeavesOutAnEmailItDoesNotHave() {
        compose.setContent { Content(account = account.copy(email = null)) }

        val server = string(R.string.preflight_row_state, string(R.string.help_server), "tracker.example.com")
        compose.onNodeWithContentDescription(server).assertExists()
    }

    @Test
    fun signOut_asksFirstAndCancelSignsNobodyOut() {
        compose.setContent { Content() }

        askToSignOut()
        compose.onNodeWithText(string(R.string.account_sign_out_title)).assertIsDisplayed()
        compose.onNodeWithText(string(R.string.account_sign_out_message)).assertIsDisplayed()
        inDialog(R.string.account_sign_out_cancel).performClick()

        compose.onNodeWithText(string(R.string.account_sign_out_title)).assertDoesNotExist()
        assertEquals(0, calls.signOuts)
    }

    @Test
    fun signOut_signsOutOnceConfirmed() {
        compose.setContent { Content() }

        askToSignOut()
        inDialog(R.string.account_sign_out).performClick()

        assertEquals(1, calls.signOuts)
    }

    @Test
    fun signOut_holdsTheQuestionUpWhileSigningOut() {
        var state by mutableStateOf(account)
        compose.setContent { Content(account = state) }
        askToSignOut()

        state = state.copy(signingOut = true)
        compose.waitForIdle()

        val spinner = SemanticsMatcher.expectValue(SemanticsProperties.ProgressBarRangeInfo, ProgressBarRangeInfo.Indeterminate)
        compose.onNode(spinner and hasAnyAncestor(isDialog()), useUnmergedTree = true).assertExists()
        inDialog(R.string.account_sign_out).assertIsNotEnabled()
        inDialog(R.string.account_sign_out_cancel).assertIsNotEnabled()
        InstrumentationRegistry.getInstrumentation().sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        compose.waitForIdle()
        compose.onNodeWithText(string(R.string.account_sign_out_title)).assertIsDisplayed()
    }

    @Test
    fun signOut_keepsTheQuestionThroughARotation() {
        val restoration = StateRestorationTester(compose)
        restoration.setContent { Content() }
        askToSignOut()

        restoration.emulateSavedInstanceStateRestore()

        compose.onNodeWithText(string(R.string.account_sign_out_title)).assertIsDisplayed()
    }

    @Test
    fun signOut_isReportedOnce() {
        var state by mutableStateOf(account.copy(signingOut = true))
        compose.setContent { Content(account = state) }

        state = state.copy(signedOut = true)
        compose.waitForIdle()
        state = state.copy(email = null)
        compose.waitForIdle()

        assertEquals(1, calls.signedOut)
    }

    // --- The account button that opens it ---

    @Test
    fun theAccountButtonOpensSettings_andWaitsWhileATripStarts() {
        var enabled by mutableStateOf(true)
        var opened = 0
        compose.setContent { CompositionLocalProvider(LocalOpenSettings provides { opened++ }) { AccountButton(enabled) } }
        val button = compose.onNodeWithContentDescription(string(R.string.settings_title))

        button.performClick()
        assertEquals(1, opened)

        enabled = false
        compose.waitForIdle()
        button.assertIsNotEnabled()
    }

    // --- The rows ---

    @Test
    fun helpAndShareAreButtons_andTheVersionIsNot() {
        compose.setContent { Content() }

        compose.onNode(hasText(string(R.string.help_title)) and hasClickAction()).performScrollTo().performClick()
        compose.onNode(hasText(string(R.string.help_share)) and hasClickAction()).performScrollTo().performClick()
        compose.onNode(hasText(string(R.string.help_version)) and hasText("0.1.0 (1)") and !hasClickAction()).performScrollTo().assertIsDisplayed()

        assertEquals(listOf(1, 1), listOf(calls.help, calls.share))
    }

    @Test
    fun theLanguageRowNamesTheLanguageShown_andOnAndroid13OpensTheAppsList() {
        assumeTrue("the app's own list comes with Android 13", Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU)
        compose.setContent { Content() }

        compose.onNode(hasText(string(R.string.language_list_title)) and hasText("English") and hasClickAction()).performScrollTo().performClick()

        compose.onNode(hasText("English") and hasAnyAncestor(isDialog())).assertExists()
    }

    @Test
    fun theThemeRowNamesTheThemeChosen_andItsListHasAllThreeWithThatOneSelected() {
        compose.setContent { Content(theme = ThemeChoice.LIGHT) }

        compose.onNode(hasText(string(R.string.settings_theme)) and hasText(string(R.string.theme_light)) and hasClickAction()).performScrollTo().performClick()

        fun choice(name: Int) = compose.onNode(hasText(string(name)) and isSelectable() and hasAnyAncestor(isDialog()))
        choice(R.string.theme_system).assert(hasText(string(R.string.theme_system_detail))).assertIsNotSelected()
        choice(R.string.theme_light).assertIsSelected()
        choice(R.string.theme_dark).assertIsNotSelected()
    }

    @Test
    fun aThemeTappedInTheListIsChosenAndTheListCloses() {
        compose.setContent { Content() }
        compose.onNode(hasText(string(R.string.settings_theme)) and hasClickAction()).performScrollTo().performClick()

        compose.onNode(hasText(string(R.string.theme_dark)) and hasAnyAncestor(isDialog())).performClick()

        assertEquals(listOf(ThemeChoice.DARK), calls.themes)
        compose.onNode(isDialog()).assertDoesNotExist()
    }

    @Test
    fun aChangeOfThemeIsInTheHeaderAtOnce() {
        var dark by mutableStateOf(false)
        compose.setContent { Content(dark = dark) }
        fun block() = compose.onRoot().captureToImage().toPixelMap()[8, 8]
        assertEquals(LightColors.primaryContainer, block())

        compose.mainClock.autoAdvance = false
        dark = true
        compose.mainClock.advanceTimeByFrame()
        compose.mainClock.advanceTimeByFrame()

        assertEquals(DarkColors.primaryContainer, block())
    }

    @Test
    fun cancelInTheThemeListChoosesNothing() {
        compose.setContent { Content() }
        compose.onNode(hasText(string(R.string.settings_theme)) and hasClickAction()).performScrollTo().performClick()

        compose.onNode(hasText(string(R.string.language_list_cancel)) and hasAnyAncestor(isDialog())).performClick()

        assertEquals(emptyList<ThemeChoice>(), calls.themes)
        compose.onNode(isDialog()).assertDoesNotExist()
    }

    @Test
    fun shareOpensTheShareSheetWithTheTextInIt() {
        val chooser = shareIntent("Vehicle Tracker diagnostics\nApp: 0.1.0 (1)")

        assertEquals(Intent.ACTION_CHOOSER, chooser.action)
        val send = requireNotNull(IntentCompat.getParcelableExtra(chooser, Intent.EXTRA_INTENT, Intent::class.java))
        assertEquals(Intent.ACTION_SEND, send.action)
        assertEquals("text/plain", send.type)
        assertEquals("Vehicle Tracker diagnostics\nApp: 0.1.0 (1)", send.getStringExtra(Intent.EXTRA_TEXT))
    }

    @Test
    fun talkBackReadsTheHeadingsInThePagesOrder() {
        compose.setContent { Box(Modifier.requiredHeight(1600.dp)) { Content() } }

        // TalkBack goes down the page, whichever is drawn last.
        val headings = compose.onAllNodes(isHeading()).fetchSemanticsNodes()
            .sortedBy { it.boundsInRoot.top }
            .map { it.config[SemanticsProperties.Text].first().text }
        assertEquals(listOf(R.string.settings_title, R.string.settings_group_phone, R.string.settings_group_app, R.string.help_section_about).map { string(it) }, headings)
    }

    // --- The largest text ---

    @Test
    fun atTheLargestTextOnTheNarrowestPhone_nothingIsCutOff() {
        compose.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale = 2f)) {
                Box(Modifier.requiredWidth(320.dp).requiredHeight(3000.dp)) { Content(ready(refused)) }
            }
        }

        compose.assertNothingIsCutOff()
        val card = compose.onNode(hasContentDescription("d@example.com", substring = true)).fetchSemanticsNode().boundsInRoot
        val signOut = compose.onNode(hasText(string(R.string.account_sign_out)) and hasClickAction()).fetchSemanticsNode().boundsInRoot
        assertTrue("Sign out goes under the email", signOut.top >= card.bottom)
    }
}
