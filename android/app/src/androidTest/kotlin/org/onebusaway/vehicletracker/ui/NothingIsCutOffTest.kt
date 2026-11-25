package org.onebusaway.vehicletracker.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.absoluteOffset
import androidx.compose.foundation.layout.requiredSize
import androidx.compose.foundation.layout.requiredWidth
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** What [assertNothingIsCutOff] lets pass, and what it does not. */
@RunWith(AndroidJUnit4::class)
class NothingIsCutOffTest {
    @get:Rule val compose = createComposeRule()

    @Composable
    private fun Page(x: Dp) {
        Box(Modifier.requiredSize(200.dp).clipToBounds()) {
            Text("Vehicle", Modifier.absoluteOffset(x).requiredWidth(120.dp))
        }
    }

    @Test
    fun textInsideThePagePasses() {
        compose.setContent { Page(x = 80.dp) }

        compose.assertNothingIsCutOff()
    }

    /** It fits its own place, and its bounds are cut where the page is, so neither says that it runs off. */
    @Test
    fun textOffItsRightFails() {
        compose.setContent { Page(x = 100.dp) }
        assertThrows(AssertionError::class.java) { compose.assertNothingIsCutOff() }
    }

    @Test
    fun textOffItsLeftFails() {
        compose.setContent { Page(x = (-20).dp) }
        assertThrows(AssertionError::class.java) { compose.assertNothingIsCutOff() }
    }
}
