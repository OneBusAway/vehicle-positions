# Preserve line numbers in stack traces for crash reporting
-keepattributes SourceFile,LineNumberTable
-renamesourcefileattribute SourceFile

# ── Retrofit ──────────────────────────────────────────────────────────────────
# Reflects on annotated interface methods at runtime
-keepattributes Signature
-keepattributes Exceptions
-keep class retrofit2.** { *; }
-keepclassmembers,allowshrinking,allowobfuscation interface * {
    @retrofit2.http.* <methods>;
}

# ── Gson ──────────────────────────────────────────────────────────────────────
# Maps JSON keys to field names — obfuscation breaks this
-keepattributes *Annotation*
-keepclassmembers class org.onebusaway.vehiclepositions.data.remote.** {
    <fields>;
}
-keep class org.onebusaway.vehiclepositions.data.remote.** { *; }
-dontwarn sun.misc.**
-keep class com.google.gson.** { *; }
-keep class * implements com.google.gson.TypeAdapterFactory
-keep class * implements com.google.gson.JsonSerializer
-keep class * implements com.google.gson.JsonDeserializer

# ── OkHttp ────────────────────────────────────────────────────────────────────
-dontwarn okhttp3.**
-dontwarn okio.**
-keep class okhttp3.** { *; }
-keep interface okhttp3.** { *; }

# ── Room ──────────────────────────────────────────────────────────────────────
# Annotation processor generates code at compile time; R8 doesn't need to touch it
-keep class androidx.room.** { *; }
-dontwarn androidx.room.paging.**

# ── Hilt ──────────────────────────────────────────────────────────────────────
-keep class dagger.hilt.** { *; }
-keep class javax.inject.** { *; }
-keep class * extends dagger.hilt.android.internal.managers.ActivityComponentManager { *; }

# ── Coroutines ────────────────────────────────────────────────────────────────
# Uses volatile fields internally for state management
-keepclassmembernames class kotlinx.** {
    volatile <fields>;
}

# ── Google Play Services ───────────────────────────────────────────────────────
-keep class com.google.android.gms.** { *; }
-dontwarn com.google.android.gms.**

# ── DataStore ─────────────────────────────────────────────────────────────────
-keep class androidx.datastore.** { *; }