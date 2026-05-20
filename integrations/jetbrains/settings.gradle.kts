// settings.gradle.kts — pulls in JetBrains IntelliJ Platform Gradle Plugin v2
// via the platform-managed plugin repository.

pluginManagement {
    repositories {
        gradlePluginPortal()
        maven("https://oss.sonatype.org/content/repositories/snapshots/")
    }
}

plugins {
    // IntelliJ Platform Plugin Repository resolver (v2 plugin requires this).
    id("org.jetbrains.intellij.platform.settings") version "2.1.0"
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.PREFER_PROJECT)
    repositories {
        mavenCentral()
        intellijPlatform {
            defaultRepositories()
        }
    }
}

rootProject.name = "eidetic-jetbrains"
