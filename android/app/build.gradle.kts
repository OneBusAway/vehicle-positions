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
        versionCode = 1
        versionName = "0.1.0"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        resValue("string", "default_server_url", defaultServerUrl)
    }

    buildTypes {
        release {
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
    // MapAssetsTest and WindowColorsTest read committed files from disk, so a change to them has to rerun the tests.
    testOptions.unitTests.all {
        it.inputs.dir("src/main/assets/map")
        it.inputs.files("src/main/res/values/colors.xml", "src/main/res/values-night/colors.xml")
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
