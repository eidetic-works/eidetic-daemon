// EideticMenubar.swift
//
// Native AppKit menubar app scaffold for eidetic-daemon. This is a SCAFFOLD
// only — no Xcode project is checked in. To build, generate an
// EideticMenubar.xcodeproj (see README), then `xcodebuild -configuration
// Release`. Signing + notarization deferred to Lokesh for the App Store path.
//
// Wire-equivalent to the SwiftBar plugin: poll /metrics on /tmp/eidetic-daemon.sock
// every 5 minutes and render `🧠 <count>` in the menubar with a dropdown menu
// of stats + quick links. AppKit version is for users who want a first-class
// app with launch-at-login, code signing, and Sparkle update channel — none
// of which SwiftBar gives them.

import AppKit
import Foundation

// MARK: - Constants (mirror eidetic-status.5m.swift)

let socketPath        = "/tmp/eidetic-daemon.sock"
let dataDir           = "\(NSHomeDirectory())/.eidetic"
let syncStateFile     = "\(dataDir)/sync-state.json"
let dashboardURL      = URL(string: "https://eidetic.works/dashboard")!
let githubRepoURL     = URL(string: "https://github.com/eidetic-works/eidetic-daemon")!
let launchctlLabel    = "works.eidetic.eideticd"
let refreshInterval: TimeInterval = 300  // 5 min

// MARK: - Daemon metrics (v0.0.7+ schema)

struct Metrics: Decodable {
    let version: String
    let uptime_seconds: Int64
    let engram_total: Int64
    let engram_by_surface: [String: Int64]
    let db_path: String
    let db_size_bytes: Int64
    let latest_version: String?
    let update_available: Bool?
}

// MARK: - AppDelegate

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var refreshTimer: Timer?

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.title = "🧠 …"
        refresh()
        refreshTimer = Timer.scheduledTimer(withTimeInterval: refreshInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
    }

    private func refresh() {
        let metrics = fetchMetrics()
        let menu = NSMenu()

        if let m = metrics {
            statusItem.button?.title = "🧠 \(humanCount(m.engram_total))"

            menu.addItem(NSMenuItem(title: "Engrams: \(m.engram_total)", action: nil, keyEquivalent: ""))
            for (surface, n) in m.engram_by_surface.sorted(by: { $0.value > $1.value }) {
                menu.addItem(NSMenuItem(title: "  \(surface): \(n)", action: nil, keyEquivalent: ""))
            }
            menu.addItem(.separator())
            menu.addItem(NSMenuItem(title: "DB: \(humanBytes(m.db_size_bytes))", action: nil, keyEquivalent: ""))
            menu.addItem(NSMenuItem(title: "Uptime: \(humanUptime(m.uptime_seconds))", action: nil, keyEquivalent: ""))
            menu.addItem(NSMenuItem(title: "Version: \(m.version)", action: nil, keyEquivalent: ""))
            if m.update_available ?? false, let latest = m.latest_version {
                menu.addItem(NSMenuItem(title: "Update available: \(latest) ⬆︎", action: #selector(openReleases), keyEquivalent: ""))
            }
        } else {
            statusItem.button?.title = "🧠 ⚠"
            menu.addItem(NSMenuItem(title: "Daemon not running", action: nil, keyEquivalent: ""))
        }

        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Open dashboard", action: #selector(openDashboard), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "GitHub repo", action: #selector(openGitHub), keyEquivalent: ""))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "Quit eideticd", action: #selector(quitDaemon), keyEquivalent: ""))
        menu.addItem(NSMenuItem(title: "Refresh", action: #selector(forceRefresh), keyEquivalent: "r"))
        menu.addItem(NSMenuItem(title: "Quit Menubar App", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))

        for item in menu.items where item.action != nil { item.target = self }
        statusItem.menu = menu
    }

    @objc private func openDashboard() { NSWorkspace.shared.open(dashboardURL) }
    @objc private func openGitHub()    { NSWorkspace.shared.open(githubRepoURL) }
    @objc private func openReleases()  { NSWorkspace.shared.open(githubRepoURL.appendingPathComponent("releases")) }
    @objc private func forceRefresh()  { refresh() }
    @objc private func quitDaemon() {
        let task = Process()
        task.launchPath = "/bin/launchctl"
        task.arguments = ["bootout", "gui/\(getuid())/\(launchctlLabel)"]
        try? task.run()
    }
}

// MARK: - Helpers

func fetchMetrics() -> Metrics? {
    let task = Process()
    task.launchPath = "/usr/bin/curl"
    task.arguments = [
        "--silent", "--max-time", "7",
        "--unix-socket", socketPath,
        "-H", "Accept: application/json",
        "http://localhost/metrics",
    ]
    let pipe = Pipe()
    task.standardOutput = pipe
    task.standardError = Pipe()
    do { try task.run() } catch { return nil }
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    task.waitUntilExit()
    guard task.terminationStatus == 0 else { return nil }
    return try? JSONDecoder().decode(Metrics.self, from: data)
}

func humanCount(_ n: Int64) -> String {
    let abs = n < 0 ? -n : n
    if abs >= 1_000_000 { return String(format: "%.1fM", Double(n) / 1_000_000) }
    if abs >= 1_000     { return String(format: "%dK", n / 1_000) }
    return "\(n)"
}

func humanBytes(_ b: Int64) -> String {
    let units = ["B", "KB", "MB", "GB", "TB"]
    var v = Double(b); var i = 0
    while v >= 1024 && i < units.count - 1 { v /= 1024; i += 1 }
    return String(format: i == 0 ? "%.0f %@" : "%.2f %@", v, units[i])
}

func humanUptime(_ seconds: Int64) -> String {
    let d = seconds / 86400, h = (seconds % 86400) / 3600, m = (seconds % 3600) / 60
    if d > 0 { return "\(d)d \(h)h" }
    if h > 0 { return "\(h)h \(m)m" }
    return "\(m)m"
}

// MARK: - Entrypoint

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
// LSUIElement=true in Info.plist suppresses Dock icon and main menu;
// app lives only in the menubar.
app.setActivationPolicy(.accessory)
app.run()
