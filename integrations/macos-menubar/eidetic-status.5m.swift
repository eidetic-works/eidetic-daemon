#!/usr/bin/env swift

// <bitbar.title>Eidetic Daemon Status</bitbar.title>
// <bitbar.version>v0.1.0</bitbar.version>
// <bitbar.author>Lokesh Garg</bitbar.author>
// <bitbar.author.github>eidetic-works</bitbar.author.github>
// <bitbar.desc>Shows engram count, DB size, uptime, last sync, and update status for the eidetic-daemon.</bitbar.desc>
// <bitbar.image>https://eidetic.works/og-eidetic-works-nucleus.svg</bitbar.image>
// <bitbar.dependencies>swift,curl,launchctl</bitbar.dependencies>
// <bitbar.abouturl>https://github.com/eidetic-works/eidetic-daemon</bitbar.abouturl>
//
// SwiftBar metadata:
// <swiftbar.hideAbout>false</swiftbar.hideAbout>
// <swiftbar.hideRunInTerminal>true</swiftbar.hideRunInTerminal>
// <swiftbar.hideSwiftBar>false</swiftbar.hideSwiftBar>
// <swiftbar.refreshOnOpen>true</swiftbar.refreshOnOpen>
//
// Drop into ~/Library/Application Support/SwiftBar/Plugins/ and the
// filename suffix `.5m.swift` tells SwiftBar to refresh every 5 minutes.

import Foundation

// MARK: - Configuration

let socketPath           = "/tmp/eidetic-daemon.sock"
let dataDir              = "\(NSHomeDirectory())/.eidetic"
let syncStateFile        = "\(dataDir)/sync-state.json"
let dashboardURL         = "https://eidetic.works/dashboard"
let githubRepoURL        = "https://github.com/eidetic-works/eidetic-daemon"
let installCommand       = "/usr/local/bin/eideticd"      // fallback "eideticd -install"
let launchctlLabel       = "works.eidetic.eideticd"

// MARK: - Daemon metrics (v0.0.7+ schema, additive)

struct Metrics: Decodable {
    let version: String
    let uptime_seconds: Int64
    let engram_total: Int64
    let engram_by_surface: [String: Int64]
    let capture_skipped: UInt64
    let db_path: String
    let db_size_bytes: Int64
    let query_p50_us: Double?
    let query_p95_us: Double?
    let query_p99_us: Double?
    let query_count: Int?
    let latest_version: String?
    let update_available: Bool?
}

struct SyncState: Decodable {
    let last_sync: String?
    let last_key: String?
    let last_bytes: Int64?
}

// MARK: - Helpers

@discardableResult
func runShell(_ launchPath: String, _ args: [String]) -> (stdout: String, exit: Int32) {
    let task = Process()
    task.launchPath = launchPath
    task.arguments = args

    let pipe = Pipe()
    task.standardOutput = pipe
    task.standardError = Pipe()

    do {
        try task.run()
    } catch {
        return ("", -1)
    }

    // Read to EOF FIRST (drains pipe), then waitUntilExit. Reversed order
    // can deadlock if stdout overflows the 64 KB pipe buffer before exit.
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    task.waitUntilExit()
    return (String(data: data, encoding: .utf8) ?? "", task.terminationStatus)
}

/// Fetch /metrics over the UDS via `curl --unix-socket`. On macOS curl is
/// pre-installed and supports `--unix-socket` (libcurl ≥ 7.40). Falling
/// back to a raw socket would require ~80 lines of CFSocket boilerplate
/// for a SwiftBar plugin that re-runs every 5 minutes, so curl is the
/// pragmatic choice.
func fetchMetrics() -> Metrics? {
    let (out, code) = runShell(
        "/usr/bin/curl",
        [
            "--silent",
            "--max-time", "7",
            "--unix-socket", socketPath,
            "-H", "Accept: application/json",
            "http://localhost/metrics",
        ]
    )
    guard code == 0, let data = out.data(using: .utf8) else { return nil }
    return try? JSONDecoder().decode(Metrics.self, from: data)
}

func fetchSyncState() -> SyncState? {
    guard let data = FileManager.default.contents(atPath: syncStateFile) else { return nil }
    return try? JSONDecoder().decode(SyncState.self, from: data)
}

func humanCount(_ n: Int64) -> String {
    let abs = n < 0 ? -n : n
    if abs >= 1_000_000 { return String(format: "%.1fM", Double(n) / 1_000_000) }
    if abs >= 1_000     { return String(format: "%dK", n / 1_000) }
    return "\(n)"
}

func humanBytes(_ b: Int64) -> String {
    let units = ["B", "KB", "MB", "GB", "TB"]
    var v = Double(b)
    var i = 0
    while v >= 1024 && i < units.count - 1 { v /= 1024; i += 1 }
    return String(format: i == 0 ? "%.0f %@" : "%.2f %@", v, units[i])
}

func humanUptime(_ seconds: Int64) -> String {
    let d = seconds / 86400
    let h = (seconds % 86400) / 3600
    let m = (seconds % 3600) / 60
    if d > 0 { return "\(d)d \(h)h" }
    if h > 0 { return "\(h)h \(m)m" }
    return "\(m)m"
}

func humanSince(_ iso: String?) -> String {
    guard let iso = iso else { return "never" }
    let fmt = ISO8601DateFormatter()
    fmt.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    var date = fmt.date(from: iso)
    if date == nil {
        fmt.formatOptions = [.withInternetDateTime]
        date = fmt.date(from: iso)
    }
    guard let d = date else { return iso }
    let delta = Int(Date().timeIntervalSince(d))
    if delta < 60       { return "\(delta)s ago" }
    if delta < 3600     { return "\(delta / 60)m ago" }
    if delta < 86400    { return "\(delta / 3600)h ago" }
    return "\(delta / 86400)d ago"
}

/// SwiftBar params after `|` use plain key=value pairs and require shell-safe
/// quoting of strings that contain spaces. URLs and paths here are simple
/// enough that no escaping is needed.
func println(_ s: String = "") { print(s) }

// MARK: - Render

let metrics = fetchMetrics()

if metrics == nil {
    // Degraded mode
    println("🧠 ⚠ | color=orange")
    println("---")
    println("Daemon not running | color=red")
    println("Install / start eideticd | bash=\(installCommand) param1=-install terminal=false")
    println("---")
    println("Open dashboard | href=\(dashboardURL)")
    println("GitHub | href=\(githubRepoURL)")
    println("Refresh | refresh=true")
    exit(0)
}

let m = metrics!
let total = humanCount(m.engram_total)
let updateIndicator = (m.update_available ?? false) ? " ⬆︎" : ""

println("🧠 \(total)\(updateIndicator) | size=12")
println("---")
println("Engrams: \(m.engram_total) | font=Menlo")
let surfaces = m.engram_by_surface.sorted { $0.value > $1.value }
for (surface, n) in surfaces {
    println("  \(surface): \(n) | font=Menlo size=11")
}
println("---")
println("DB: \(humanBytes(m.db_size_bytes)) | font=Menlo size=11")
println("  \(m.db_path) | font=Menlo size=10 color=gray")
println("Uptime: \(humanUptime(m.uptime_seconds)) | font=Menlo size=11")
println("Version: \(m.version) | font=Menlo size=11")
if let p95 = m.query_p95_us {
    println("Query p95: \(String(format: "%.1f", p95 / 1000)) ms | font=Menlo size=11")
}

println("---")
let sync = fetchSyncState()
println("Last sync: \(humanSince(sync?.last_sync)) | font=Menlo size=11")
if let bytes = sync?.last_bytes, bytes > 0 {
    println("  Last upload: \(humanBytes(bytes)) | font=Menlo size=10 color=gray")
}

println("---")
if m.update_available ?? false, let latest = m.latest_version {
    println("Update available: \(latest) ⬆︎ | color=orange href=\(githubRepoURL)/releases")
} else {
    println("Up to date | size=11 color=gray")
}

println("---")
println("Open dashboard | href=\(dashboardURL)")
println("GitHub repo | href=\(githubRepoURL)")
println("---")
println("Quit eideticd | bash=/bin/launchctl param1=bootout param2=gui/\(getuid())/\(launchctlLabel) terminal=false refresh=true")
println("Refresh | refresh=true")
