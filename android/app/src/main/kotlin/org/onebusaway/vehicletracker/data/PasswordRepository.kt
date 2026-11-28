package org.onebusaway.vehicletracker.data

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.onebusaway.vehicletracker.data.api.ChangePasswordRequest
import org.onebusaway.vehicletracker.data.api.RefreshTokens
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import retrofit2.HttpException
import java.io.IOException
import javax.inject.Inject

private const val HTTP_UNAUTHORIZED = 401
private const val HTTP_NOT_FOUND = 404
private const val HTTP_CONFLICT = 409
private const val HTTP_UNPROCESSABLE = 422
private const val HTTP_TOO_MANY_REQUESTS = 429

/** Why a password change did not go through, as Change password tells it apart. */
enum class PasswordChangeFailure {
    /** A 422: the current password is not the driver's. */
    WRONG_CURRENT,

    /** A 429: wrong passwords count with the account's failed sign-ins. */
    TOO_MANY_TRIES,

    /** Nothing answered. */
    UNREACHABLE,

    /** A 404: the server is older than the change. */
    OLDER_SERVER,

    /** A 401, or no refresh token to keep this phone signed in: the sign-in is over. */
    SESSION_OVER,

    /** Any other answer: a 5xx, a 403, a 400, or a second 409. */
    FAILED,
}

/** Changes the driver's password: null once changed, or why it was not. */
fun interface PasswordChanger {
    suspend fun change(current: String, new: String): PasswordChangeFailure?
}

/**
 * Changes the driver's own password (`POST /api/v1/auth/password`). The request names this phone's refresh token,
 * which the server keeps good while it ends every other sign-in of the driver, so this phone stays signed in: the
 * access token the change ended is refreshed by the next request.
 */
class PasswordRepository @Inject constructor(
    private val apiProvider: TrackerApiProvider,
    private val refreshTokens: RefreshTokens,
) : PasswordChanger {
    /**
     * Null once the password is changed, or why it was not. The refresh token is read when no refresh is under way,
     * as one under way may be spending it. A 409 says it was spent on the way all the same, by a refresh for another
     * request: then it is sent once more, with the new one.
     */
    override suspend fun change(current: String, new: String): PasswordChangeFailure? = withContext(Dispatchers.IO) {
        try {
            // With no refresh token this sign-in can't outlive the change.
            val refreshToken = refreshTokens.settledRefreshToken() ?: return@withContext PasswordChangeFailure.SESSION_OVER
            try {
                send(current, new, refreshToken)
            } catch (e: HttpException) {
                if (e.code() != HTTP_CONFLICT) throw e
                val renewed = refreshTokens.refreshTokenAfterConflict(refreshToken) ?: return@withContext PasswordChangeFailure.SESSION_OVER
                // A second 409 is the caller's.
                send(current, new, renewed)
            }
            null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            passwordChangeFailureOf(e)
        }
    }

    private suspend fun send(current: String, new: String, refreshToken: String) =
        apiProvider.get().changePassword(ChangePasswordRequest(current, new, refreshToken))
}

internal fun passwordChangeFailureOf(e: Exception): PasswordChangeFailure = when {
    e is HttpException && e.code() == HTTP_UNPROCESSABLE -> PasswordChangeFailure.WRONG_CURRENT
    e is HttpException && e.code() == HTTP_TOO_MANY_REQUESTS -> PasswordChangeFailure.TOO_MANY_TRIES
    e is HttpException && e.code() == HTTP_NOT_FOUND -> PasswordChangeFailure.OLDER_SERVER
    e is HttpException && e.code() == HTTP_UNAUTHORIZED -> PasswordChangeFailure.SESSION_OVER
    e is IOException -> PasswordChangeFailure.UNREACHABLE
    else -> PasswordChangeFailure.FAILED
}
