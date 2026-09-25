package org.onebusaway.vehicletracker.engine

import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import java.time.Instant
import java.time.ZoneId

/**
 * One trip as the phone judges adherence against it (spec §5.5): the shape, the stops with
 * absolute scheduled times, and the thresholds the server applies, so the phone judges by the
 * same numbers. Read from `GET /api/v1/gtfs/trips/{trip_id}`, and serializable so the phone can
 * keep it for the length of the trip.
 */
@Serializable
data class TripGeometry(
    val tripId: String,
    /** The service date the stop times fall on, YYYYMMDD. */
    val serviceDate: String,
    /** The agency's zone: every clock time the driver is shown is read in it. */
    @Serializable(with = ZoneIdSerializer::class) val timezone: ZoneId,
    val shapePoints: List<GeoPoint>,
    val stops: List<TripStop>,
    val thresholds: AdherenceThresholds,
)

@Serializable
data class TripStop(
    val id: String,
    val name: String,
    val lat: Double,
    val lon: Double,
    val alongShapeM: Double,
    @Serializable(with = InstantSerializer::class) val arrivalAt: Instant,
    @Serializable(with = InstantSerializer::class) val departureAt: Instant,
)

/** The server's own adherence rules; the phone's display cut-offs live in [AdherenceEvaluator]. */
@Serializable
data class AdherenceThresholds(
    val maxShapeDistanceM: Double,
    val scheduleEarlyS: Int,
    val scheduleLateS: Int,
)

private object InstantSerializer : KSerializer<Instant> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("java.time.Instant", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: Instant) = encoder.encodeString(value.toString())
    override fun deserialize(decoder: Decoder): Instant = Instant.parse(decoder.decodeString())
}

private object ZoneIdSerializer : KSerializer<ZoneId> {
    override val descriptor: SerialDescriptor = PrimitiveSerialDescriptor("java.time.ZoneId", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: ZoneId) = encoder.encodeString(value.id)
    override fun deserialize(decoder: Decoder): ZoneId = ZoneId.of(decoder.decodeString())
}
