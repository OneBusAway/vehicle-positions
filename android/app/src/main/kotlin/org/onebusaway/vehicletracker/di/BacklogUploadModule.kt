package org.onebusaway.vehicletracker.di

import dagger.Binds
import dagger.Module
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import org.onebusaway.vehicletracker.service.BacklogUploadScheduler
import org.onebusaway.vehicletracker.service.JobBacklogUploadScheduler

@Module
@InstallIn(SingletonComponent::class)
abstract class BacklogUploadModule {
    @Binds
    abstract fun bindBacklogUploadScheduler(scheduler: JobBacklogUploadScheduler): BacklogUploadScheduler
}
