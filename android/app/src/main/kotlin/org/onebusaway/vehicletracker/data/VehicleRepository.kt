package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.data.api.VehicleDto
import retrofit2.HttpException
import javax.inject.Inject

private const val HTTP_FORBIDDEN = 403

class VehicleRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val sessionStore: SessionStore,
) {
    // apiProvider.get() is called here (not injected as a resolved TrackerApi) so that a missing
    // server URL (e.g. cold start racing session restore) surfaces as Result.failure instead of
    // an uncaught exception during construction.
    suspend fun myVehicles(): Result<List<VehicleDto>> = try {
        Result.success(apiProvider.get().myVehicles())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(if (refusedAViewer(e)) ApiError.ViewerAccount else mapHttpError(e))
    }

    /**
     * The driver API refuses a viewer with a 403, on this list first. A viewer's session can be stored here although
     * Sign in no longer keeps one: one signed in before this build, or a driver's account made a viewer since, whose
     * next refreshed token says so. Told by the token's role, so another 403 still reads as it did.
     */
    private suspend fun refusedAViewer(e: Exception): Boolean =
        e is HttpException && e.code() == HTTP_FORBIDDEN && sessionStore.session.first().isViewer
}
