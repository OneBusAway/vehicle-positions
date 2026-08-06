package org.onebusaway.vehicletracker.di

import android.content.Context
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.runBlocking
import org.onebusaway.vehicletracker.data.DataStoreSessionStore
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApi
import org.onebusaway.vehicletracker.data.api.TrackerApiProvider
import org.onebusaway.vehicletracker.service.ServiceController
import org.onebusaway.vehicletracker.service.ServiceControllerImpl
import javax.inject.Qualifier
import javax.inject.Singleton

/** Qualifies the injected `() -> Long` clock lambda (epoch seconds) so the binding is unambiguous. */
@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class EpochSecondsClock

/**
 * Keeps a `@Volatile` cache of the current auth token + server URL in sync with [SessionStore],
 * so [TrackerApi] calls can read them synchronously (Retrofit interceptors are not suspending).
 * The [TrackerApi] instance is rebuilt only when the server URL changes.
 *
 * The cache is normally kept fresh by [scope] collecting [SessionStore.session] in the
 * background. On a cold start that collector may not have delivered its first emission yet by
 * the time [api] is first called (e.g. `VehicleScreen`'s ViewModel is constructed immediately
 * after a fresh login token was persisted) — [api] guards against that race by synchronously
 * seeding itself from the store exactly once, the first time it's called, via [ensureSeeded].
 */
class ApiHolder(private val sessionStore: SessionStore, scope: CoroutineScope) {
    @Volatile private var token: String? = null
    @Volatile private var serverUrl: String? = null
    @Volatile private var seeded = false
    @Volatile private var cachedApi: TrackerApi? = null
    @Volatile private var cachedApiUrl: String? = null
    private val seedLock = Any()

    val apiFactory = ApiFactory { token }

    init {
        sessionStore.session
            .onEach { session ->
                token = session.token
                serverUrl = session.serverUrl
                seeded = true
            }
            .launchIn(scope)
    }

    /**
     * Blocks (briefly — [SessionStore.session] reads from an already-warm in-memory DataStore
     * cache) only the first time it's called and only if the background collector in [init]
     * hasn't emitted yet. Never throws: a failure to read the store just leaves the cache empty,
     * which [api] then reports as a normal, catchable failure rather than a crash.
     */
    private fun ensureSeeded() {
        if (seeded) return
        synchronized(seedLock) {
            if (seeded) return
            runCatching { runBlocking { sessionStore.session.first() } }
                .onSuccess { session ->
                    token = session.token
                    serverUrl = session.serverUrl
                }
            seeded = true
        }
    }

    /**
     * Returns a [TrackerApi] bound to the current server URL. Throws only when the URL is
     * genuinely absent (never logged in / no session persisted) — callers are expected to invoke
     * this from within their own try/catch (as [org.onebusaway.vehicletracker.data.VehicleRepository]
     * and [org.onebusaway.vehicletracker.data.TripRepository] do) so that case surfaces as
     * `Result.failure` rather than an uncaught exception.
     */
    fun api(): TrackerApi {
        ensureSeeded()
        val url = serverUrl.orEmpty()
        val existing = cachedApi
        if (existing != null && cachedApiUrl == url) return existing
        return apiFactory.create(url).also {
            cachedApi = it
            cachedApiUrl = url
        }
    }
}

@Module
@InstallIn(SingletonComponent::class)
object AppModule {
    @Provides
    @Singleton
    fun provideCoroutineScope(): CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    @Provides
    @Singleton
    fun provideSessionStore(@ApplicationContext context: Context): SessionStore =
        DataStoreSessionStore(context)

    @Provides
    @Singleton
    fun provideTripStateStore(@ApplicationContext context: Context): TripStateStore =
        DataStoreTripStateStore(context)

    @Provides
    @Singleton
    fun provideApiHolder(sessionStore: SessionStore, scope: CoroutineScope): ApiHolder =
        ApiHolder(sessionStore, scope)

    @Provides
    @Singleton
    fun provideApiFactory(holder: ApiHolder): ApiFactory = holder.apiFactory

    /**
     * A lazy accessor for repositories (`VehicleRepository`, `TripRepository`) that must not
     * eagerly resolve [TrackerApi] at Hilt-graph-construction time — [ApiHolder.api] can throw
     * if the server URL is genuinely absent, and eager resolution would let that exception
     * escape uncaught during ViewModel construction. Deferring the call to inside the
     * repository's own suspend function lets its existing try/catch convert it into
     * `Result.failure`.
     */
    @Provides
    fun provideTrackerApiProvider(holder: ApiHolder): TrackerApiProvider = TrackerApiProvider(holder::api)

    @Provides
    @EpochSecondsClock
    fun provideClock(): () -> Long = { System.currentTimeMillis() / 1000 }

    @Provides
    @Singleton
    fun provideServiceController(@ApplicationContext context: Context): ServiceController =
        ServiceControllerImpl(context)
}
