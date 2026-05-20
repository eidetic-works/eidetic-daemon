// build.gradle.kts — IntelliJ Platform Gradle Plugin v2 build script.
//
// Run `./gradlew buildPlugin` to produce a distributable zip under
// build/distributions/eidetic-jetbrains-<version>.zip.
//
// The `intellijPlatform` block is the v2 DSL — it replaces the older
// `intellij { ... }` block from the v1 plugin. Versions are read from
// gradle.properties so they stay in one place.

import org.jetbrains.intellij.platform.gradle.TestFrameworkType

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "2.0.21"
    id("org.jetbrains.intellij.platform") version "2.1.0"
}

group = providers.gradleProperty("pluginGroup").get()
version = providers.gradleProperty("pluginVersion").get()

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(17))
    }
}

kotlin {
    jvmToolchain(17)
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

// IntelliJ Platform Gradle Plugin v2 dependencies block. The platform jars
// are pulled in here and exposed to compileKotlin via the platform classpath.
dependencies {
    intellijPlatform {
        // Build against IntelliJ IDEA Community 2024.1 (broadest API surface).
        create(
            providers.gradleProperty("platformType").get(),
            providers.gradleProperty("platformVersion").get()
        )

        // Plugin verifier + instrumentation cookbook (recommended by JB docs).
        pluginVerifier()
        zipSigner()
        instrumentationTools()

        // No bundled plugin deps — we use only platform.* APIs.

        testFramework(TestFrameworkType.Platform)
    }

    // Jackson Kotlin module — the IntelliJ Platform bundles Jackson core, but
    // the Kotlin module (which gives data-class auto-binding) is not always
    // on the public API surface. Pin it here so DaemonClient compiles
    // identically across platform versions.
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.17.0")
}

intellijPlatform {
    pluginConfiguration {
        name = providers.gradleProperty("pluginName")
        version = providers.gradleProperty("pluginVersion")

        ideaVersion {
            sinceBuild = providers.gradleProperty("pluginSinceBuild")
            // untilBuild left blank in gradle.properties → emit empty so the
            // IDE updater treats this plugin as compatible with future builds.
            untilBuild = providers.gradleProperty("pluginUntilBuild").orElse("")
        }
    }

    // We're not signing or publishing from this scaffold — leave the signing
    // and publishing blocks unconfigured. CI can fill them in via env vars
    // (CERTIFICATE_CHAIN / PRIVATE_KEY / PUBLISH_TOKEN) when the time comes.
}

tasks {
    wrapper {
        gradleVersion = "8.10"
    }

    // Patch the plugin.xml at package time so version/since/until come from
    // gradle.properties (single source of truth). The v2 plugin does this
    // automatically via pluginConfiguration above; this block is here as a
    // hook for future custom patchers.

    runIde {
        // Increase heap for the sandbox IDE — JetBrains' defaults are tight.
        jvmArgs("-Xmx2g")
    }
}
