# Spike: Tauri-Rust Cross-Compile linux+windows Runtime Validation

**Date:** 2026-05-19  
**Directive:** cc-tb SPIKE-DIRECTIVE  
**Status:** COMPLETE — Tauri NOT needed

## Findings

### Go cross-compile: already works perfectly

```
PASS [darwin-arm64]: eideticd-darwin-arm64  (15.2 MB)
PASS [linux-amd64]:  eideticd-linux-amd64   (15.6 MB)
PASS [windows-amd64]: eideticd-windows-amd64.exe (16.1 MB)
```

Pure-Go + `modernc.org/sqlite` (no CGO) → all 3 platforms compile from macOS with `make build-all`. Zero linker config, zero cross-toolchain needed. Windows `.exe` and `.tar.gz` added to v0.0.26 GitHub release.

### Tauri viability: overkill for daemon stage

| Dimension | Go binary | Tauri |
|---|---|---|
| Size | 15-16 MB | +50MB+ webview |
| Linux cross-compile | trivial | requires webkit2gtk system dep |
| Windows cross-compile | trivial | requires MSVC or complex zigbuild |
| Install surface | Homebrew / shell / systemd | installer .exe / .dmg / .AppImage |
| GUI required | no | yes (forces a render process) |

The daemon is a background service. No user-facing window. Tauri's value proposition (native GUI shell) doesn't apply here.

**What Tauri would add:** system tray icon + auto-updater + native installer.  
**Cost:** 5x binary size, webkit2gtk runtime dep on Linux, complex CI matrix.  
**When to revisit:** when there are >100 users and "no visible status" tops support complaints.

### Rust cross-compile (cargo-zigbuild): installed but deferred

- `cargo-zigbuild v0.22.3` installed  
- `zig 0.16.0` installing (llvm dep, ~207 MB — ongoing)
- `x86_64-unknown-linux-gnu` + `x86_64-pc-windows-gnu` Rust std targets installed  
- Minimal launcher scaffold at `/tmp/tauri-spike/` — not built (unnecessary given finding above)

## Decision

**Ship Go binaries. Skip Tauri. Revisit if GUI demand emerges post-100 users.**

Immediate action taken: added `eideticd-windows-amd64.exe` + `.tar.gz` to v0.0.26 release. Windows users can now install manually — Chocolatey/Scoop package is the next packaging step (not Tauri).
