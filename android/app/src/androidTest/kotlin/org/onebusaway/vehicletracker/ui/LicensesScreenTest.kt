package org.onebusaway.vehicletracker.ui

import androidx.compose.material3.Surface
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasScrollToIndexAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isHeading
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.onebusaway.vehicletracker.ui.settings.LicenseScreen
import org.onebusaway.vehicletracker.ui.settings.LicensesScreen
import org.onebusaway.vehicletracker.ui.theme.AppTheme

/** Licenses: each library and font with its license, and a license's full text, read from the app's assets. */
@RunWith(AndroidJUnit4::class)
class LicensesScreenTest {
    @get:Rule val compose = createComposeRule()

    private val names = org.onebusaway.vehicletracker.ui.settings.Licenses.map { it.name }

    @Test
    fun eachIsARowWithItsLicense_andATapOpensIt() {
        val opened = mutableListOf<Int>()
        compose.setContent { AppTheme(darkTheme = false) { Surface { LicensesScreen(onBack = {}, onOpen = { opened += it }) } } }

        val list = compose.onNode(hasScrollToIndexAction())
        for ((index, name) in names.withIndex()) {
            list.performScrollToNode(hasText(name))
            compose.onNode(hasText(name) and hasClickAction()).performClick()
        }

        assertEquals(names.indices.toList(), opened)
    }

    @Test
    fun aLicenseShowsItsTextFromTheApp() {
        val index = names.indexOf("MapLibre Java (GeoJSON and Turf)")
        compose.setContent { AppTheme(darkTheme = false) { Surface { LicenseScreen(index, onBack = {}) } } }

        compose.onNode(hasText(names[index]) and isHeading()).assertIsDisplayed()
        compose.waitUntil(5_000) { compose.onAllNodes(hasText("The MIT License (MIT)")).fetchSemanticsNodes().isNotEmpty() }
        compose.onNodeWithText("Copyright (c) 2018 Mapbox").assertIsDisplayed()
    }

    // Hundreds of pages, read off the main thread and shown a paragraph at a time.
    @Test
    fun theLongestLicenseOpens_andScrollsToItsEnd() {
        val index = names.indexOf("Google Play services location")
        compose.setContent { AppTheme(darkTheme = false) { Surface { LicenseScreen(index, onBack = {}) } } }

        compose.waitUntil(10_000) { compose.onAllNodes(hasText("Android Software Development Kit License", substring = true)).fetchSemanticsNodes().isNotEmpty() }
        compose.onNode(hasScrollToIndexAction()).performScrollToNode(hasText("Tink"))
        compose.onNodeWithText("Tink").assertIsDisplayed()
    }
}
