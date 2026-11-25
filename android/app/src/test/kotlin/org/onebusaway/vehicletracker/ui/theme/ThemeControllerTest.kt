package org.onebusaway.vehicletracker.ui.theme

import android.content.res.Configuration
import android.os.Build
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.onebusaway.vehicletracker.data.ThemeChoice
import org.onebusaway.vehicletracker.data.ThemeStore

/** Which of Android and the activity gives the app its theme, and what each is told. */
class ThemeControllerTest {
    private class Stored(override var choice: ThemeChoice) : ThemeStore

    private val set = mutableListOf<ThemeChoice>()

    private fun controller(sdk: Int, store: ThemeStore) = ThemeController(sdk, store, set::add)

    @Test
    fun `from Android 12 a choice is stored and Android is told, and the activity is left as it is`() {
        val store = Stored(ThemeChoice.SYSTEM)
        assertFalse(controller(Build.VERSION_CODES.S, store).choose(ThemeChoice.DARK))
        assertEquals(ThemeChoice.DARK, store.choice)
        assertEquals(listOf(ThemeChoice.DARK), set)
    }

    @Test
    fun `below Android 12 a choice is stored and the activity is to be made again`() {
        val store = Stored(ThemeChoice.SYSTEM)
        assertTrue(controller(Build.VERSION_CODES.R, store).choose(ThemeChoice.DARK))
        assertEquals(ThemeChoice.DARK, store.choice)
        assertEquals(emptyList<ThemeChoice>(), set)
    }

    @Test
    fun `the theme chosen already changes nothing`() {
        for (sdk in listOf(Build.VERSION_CODES.R, Build.VERSION_CODES.S)) {
            assertFalse(controller(sdk, Stored(ThemeChoice.LIGHT)).choose(ThemeChoice.LIGHT))
        }
        assertEquals(emptyList<ThemeChoice>(), set)
    }

    @Test
    fun `as the process starts Android is told again, as it was not of a restored choice`() {
        controller(Build.VERSION_CODES.S, Stored(ThemeChoice.DARK)).start()
        assertEquals(listOf(ThemeChoice.DARK), set)
    }

    @Test
    fun `below Android 12 there is nothing to tell it`() {
        controller(Build.VERSION_CODES.R, Stored(ThemeChoice.DARK)).start()
        assertEquals(emptyList<ThemeChoice>(), set)
    }

    @Test
    fun `an activity has nothing to set from Android 12, nor below it with the phone's own theme`() {
        for (choice in ThemeChoice.entries) assertNull(controller(Build.VERSION_CODES.S, Stored(choice)).activityConfiguration())
        assertNull(controller(Build.VERSION_CODES.R, Stored(ThemeChoice.SYSTEM)).activityConfiguration())
    }

    @Test
    fun `each theme's night bits, and none of them for the phone's own`() {
        assertEquals(Configuration.UI_MODE_NIGHT_UNDEFINED, nightBits(ThemeChoice.SYSTEM))
        assertEquals(Configuration.UI_MODE_NIGHT_NO, nightBits(ThemeChoice.LIGHT))
        assertEquals(Configuration.UI_MODE_NIGHT_YES, nightBits(ThemeChoice.DARK))
    }
}
