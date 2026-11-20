plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.ksp)
    alias(libs.plugins.hilt)
}

// An agency building its own app can give Sign in a server to start from, which the driver can still change:
// ./gradlew assembleRelease -PdefaultServerUrl=https://tracker.example.org (docs/deployment.md, section 13.2).
val defaultServerUrl = (findProperty("defaultServerUrl") as String?).orEmpty()
if (defaultServerUrl.isNotEmpty() && !defaultServerUrl.startsWith("https://")) {
    throw GradleException("defaultServerUrl must be an https:// address, or empty for no default")
}

android {
    namespace = "org.onebusaway.vehicletracker"
    compileSdk = 37

    defaultConfig {
        applicationId = "org.onebusaway.vehicletracker"
        minSdk = 26
        targetSdk = 36
        // Set per build without editing this file: `./gradlew -PversionCode=42 -PversionName=1.2.0`,
        // or the same keys in ~/.gradle/gradle.properties. CI builds pass -PversionCode=1 with
        // -PversionName=ci-<run_number>. The release workflow derives versionCode from the tag
        // (major * 10000 + minor * 100 + patch).
        versionCode = (project.findProperty("versionCode") as String?)?.toInt() ?: 1
        versionName = project.findProperty("versionName") as String? ?: "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        resValue("string", "default_server_url", defaultServerUrl)
    }

    // Used only when a keystore is supplied (docs/deployment.md §12.4), so a build without one,
    // CI's included, still produces app-release-unsigned.apk as before.
    val keystorePath = System.getenv("ANDROID_KEYSTORE")
    signingConfigs {
        if (keystorePath != null) {
            create("release") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    androidResources {
        // Lists the app's languages for Settings > Apps > Language on Android 13 and up, from the values-<language>
        // folders and res/resources.properties, which says values/ is English.
        generateLocaleConfig = true
        // The languages the app has its own strings in. A library's strings in any other language are left out, so a
        // phone set to one shows the app in English throughout, not English mixed with the library's words. A
        // language is added here in the same pull request as its values-<language>/strings.xml (#495).
        localeFilters += listOf("en")
    }

    buildTypes {
        debug {
            // en-XA (accented and longer) and ar-XB (right to left), for finding text strings.xml misses and layouts
            // that break in another language. A debug build offers them in Settings > Apps > Language.
            isPseudoLocalesEnabled = true
        }
        release {
            signingConfig = signingConfigs.findByName("release")
            // MapLibre's native library is about 10 MB for each ABI. Phones are ARM; the x86 ones are
            // for emulators, which run the debug build.
            ndk { abiFilters += listOf("arm64-v8a", "armeabi-v7a") }
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
        compose = true
        resValues = true
    }
    testOptions.unitTests.isReturnDefaultValues = true
    // MapAssetsTest, WindowColorsTest and StringsXmlTest read committed files from disk, so a change to them has to
    // rerun the tests.
    testOptions.unitTests.all {
        it.inputs.dir("src/main/assets/map")
        it.inputs.files(
            "src/main/res/values/colors.xml",
            "src/main/res/values-night/colors.xml",
            "src/main/res/values/strings.xml",
        )
    }
    // The translation checks (#495). New strings land in English first, so one not translated yet is a warning; a
    // translation that breaks a placeholder or a plural, or translates what must stay as it is, is an error. Compose's
    // NonObservableLocale, an error already, keeps a composable from formatting in a language it can't see change.
    lint {
        error += listOf("MissingQuantity", "UnusedQuantity", "StringFormatCount", "Untranslatable", "UnusedTranslation")
        warning += "MissingTranslation"
    }
}

dependencies {
    implementation(platform(libs.compose.bom))
    implementation(libs.core.ktx)
    implementation(libs.core.splashscreen)
    implementation(libs.activity.compose)
    implementation(libs.compose.ui)
    implementation(libs.compose.material3)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.navigation.compose)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.lifecycle.viewmodel.compose)
    implementation(libs.lifecycle.service)
    implementation(libs.hilt.android)
    ksp(libs.hilt.compiler)
    implementation(libs.hilt.navigation.compose)
    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.play.services.location)
    implementation(libs.datastore.preferences)
    implementation(libs.coroutines.android)
    implementation(libs.coroutines.play.services)
    implementation(libs.maplibre.android)

    debugImplementation(libs.compose.ui.tooling)
    debugImplementation(libs.compose.ui.test.manifest)

    testImplementation(libs.junit)
    testImplementation(libs.turbine)
    testImplementation(libs.mockwebserver)
    testImplementation(libs.coroutines.test)

    androidTestImplementation(platform(libs.compose.bom))
    androidTestImplementation(libs.compose.ui.test.junit4)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.ext.junit)
}
