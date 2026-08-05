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
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach
import org.onebusaway.vehicletracker.data.DataStoreSessionStore
import org.onebusaway.vehicletracker.data.DataStoreTripStateStore
import org.onebusaway.vehicletracker.data.SessionStore
import org.onebusaway.vehicletracker.data.TripStateStore
import org.onebusaway.vehicletracker.data.api.ApiFactory
import org.onebusaway.vehicletracker.data.api.TrackerApi
import org.onebusaway.vehicletracker.service.NoOpServiceController
import org.onebusaway.vehicletracker.service.ServiceController
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
 */
class ApiHolder(sessionStore: SessionStore, scope: CoroutineScope) {
    @Volatile private var token: String? = null
    @Volatile private var serverUrl: String? = null
    @Volatile private var cachedApi: TrackerApi? = null
    @Volatile private var cachedApiUrl: String? = null

    val apiFactory = ApiFactory { token }

    init {
        sessionStore.session
            .onEach { session ->
                token = session.token
                serverUrl = session.serverUrl
            }
            .launchIn(scope)
    }

    fun api(): TrackerApi {
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

    @Provides
    fun provideTrackerApi(holder: ApiHolder): TrackerApi = holder.api()

    @Provides
    @EpochSecondsClock
    fun provideClock(): () -> Long = { System.currentTimeMillis() / 1000 }

    @Provides
    @Singleton
    fun provideServiceController(): ServiceController = NoOpServiceController() // replaced in Task 8
}
