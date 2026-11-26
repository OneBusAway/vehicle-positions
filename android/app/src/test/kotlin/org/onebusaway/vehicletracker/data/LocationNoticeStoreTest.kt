package org.onebusaway.vehicletracker.data

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.IOException

/** [DataStoreLocationNoticeStore] against a real file-backed DataStore, as [TripSettingsStoreTest]. */
class LocationNoticeStoreTest {
    @get:Rule val tempFolder = TemporaryFolder()

    private val scope = CoroutineScope(Job() + Dispatchers.IO)

    @After fun tearDown() = scope.cancel()

    private val file by lazy { File(tempFolder.newFolder(), "location_notice.preferences_pb") }

    @Test fun `not agreed until a tap on Agree, then agreed for good`() = runTest {
        val store = DataStoreLocationNoticeStore(PreferenceDataStoreFactory.create(scope = scope, produceFile = { file }))
        assertFalse(store.agreed.first())

        store.agree()

        assertTrue(store.agreed.first())
    }

    @Test fun `a record that can't be read shows the notice again rather than crash`() = runTest {
        assertFalse(DataStoreLocationNoticeStore(FailingDataStore(IOException("disk"))).agreed.first())
    }

    @Test(expected = IOException::class)
    fun `an agreement that can't be saved says so`() = runTest {
        DataStoreLocationNoticeStore(FailingDataStore(IOException("full"))).agree()
    }
}
