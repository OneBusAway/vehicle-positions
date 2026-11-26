package org.onebusaway.vehicletracker.ui.settings

/**
 * A library or font in the app: its [name], its license's, and the asset with the license's full text. [groups] are the
 * Maven groups it stands for, so a test can tell that each library the app declares has a row.
 */
internal data class License(val name: String, val license: String, val text: String, val groups: List<String> = emptyList())

private const val APACHE = "Apache License 2.0"
private const val APACHE_TEXT = "licenses/apache-2.0.txt"

/**
 * Every library and font in the release build, from its runtime classpath, by name. Names and licenses stay in
 * English: they are the projects' own.
 */
internal val Licenses: List<License> = listOf(
    License("AndroidX and Jetpack Compose", APACHE, APACHE_TEXT, listOf("androidx")),
    License("Dagger, Hilt and Jakarta Inject", APACHE, APACHE_TEXT, listOf("com.google.dagger", "javax.inject", "jakarta.inject")),
    License(
        "Google Play services location",
        "Android Software Development Kit License",
        "licenses/google-play-services.txt",
        listOf("com.google.android.gms"),
    ),
    License("Google Sans Flex", "SIL Open Font License 1.1", "licenses/google-sans-flex-OFL.txt"),
    License("Gson", APACHE, APACHE_TEXT, listOf("com.google.code.gson")),
    License("Guava ListenableFuture", APACHE, APACHE_TEXT, listOf("com.google.guava")),
    License("JetBrains Java Annotations", APACHE, APACHE_TEXT, listOf("org.jetbrains")),
    License("JSpecify", APACHE, APACHE_TEXT, listOf("org.jspecify")),
    License("JSR 305 annotations", APACHE, APACHE_TEXT, listOf("com.google.code.findbugs")),
    License("Kotlin, coroutines and serialization", APACHE, APACHE_TEXT, listOf("org.jetbrains.kotlin", "org.jetbrains.kotlinx")),
    License("MapLibre gestures", "BSD 2-Clause License", "licenses/maplibre-gestures-android.md"),
    License("MapLibre Java (GeoJSON and Turf)", "MIT License", "licenses/maplibre-java-MIT.txt"),
    License("MapLibre Native", "BSD 2-Clause License", "licenses/maplibre-native-android.md", listOf("org.maplibre.gl")),
    License("Material Symbols", APACHE, "licenses/material-symbols-LICENSE.txt"),
    License("Noto Sans, on the street map", "SIL Open Font License 1.1", "map/fonts/OFL.txt"),
    License("OkHttp, Okio and Retrofit", APACHE, APACHE_TEXT, listOf("com.squareup.okhttp3", "com.squareup.okio", "com.squareup.retrofit2")),
    License("Protomaps basemap style", "BSD 3-Clause License", "map/LICENSE.md"),
    License("Public Suffix List, in OkHttp", "Mozilla Public License 2.0", "licenses/mpl-2.0.txt"),
    License("Timber", APACHE, APACHE_TEXT, listOf("com.jakewharton.timber")),
    License("Vehicle Tracker", APACHE, APACHE_TEXT),
)
