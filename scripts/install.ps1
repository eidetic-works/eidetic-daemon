# Eidetic Works daemon — Windows installer
#
# Usage (run in PowerShell as normal user, no admin needed):
#   irm https://eidetic.works/install.ps1 | iex
#
# Installs to: $env:LOCALAPPDATA\EideticWorks\bin\eideticd.exe
# Startup:     Registry HKCU Run key (login without admin)
# Socket:      \\.\pipe\eidetic-daemon  (Windows named pipe)
[CmdletBinding()] param()
$ErrorActionPreference = 'Stop'

$Repo    = $env:EIDETIC_REPO    ?? 'eidetic-works/eidetic-daemon'
$Version = $env:EIDETIC_VERSION ?? 'latest'
$BinDir  = Join-Path $env:LOCALAPPDATA 'EideticWorks\bin'

function Write-Log($msg) { Write-Host "install: $msg" }
function Write-Err($msg) { Write-Host "install: ERROR: $msg" -ForegroundColor Red; exit 1 }

# Arch check — only amd64 in release matrix for Windows
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
if ($arch -ne 'X64') { Write-Err "Unsupported arch: $arch — only x86_64 is in the Windows release matrix" }

# Resolve version
if ($Version -eq 'latest') {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $rel.tag_name
    Write-Log "latest: $Version"
}

$AssetName = "eideticd-windows-amd64.exe"
$Url = "https://github.com/$Repo/releases/download/$Version/$AssetName"
Write-Log "downloading $AssetName ($Version)"

# Install dir
if (-not (Test-Path $BinDir)) { New-Item -ItemType Directory -Force -Path $BinDir | Out-Null }
$ExePath = Join-Path $BinDir 'eideticd.exe'

Invoke-WebRequest -Uri $Url -OutFile $ExePath -UseBasicParsing
Write-Log "installed $ExePath"

# Add to user PATH
$CurrentPath = [System.Environment]::GetEnvironmentVariable('PATH', 'User')
if ($CurrentPath -notlike "*$BinDir*") {
    [System.Environment]::SetEnvironmentVariable('PATH', "$BinDir;$CurrentPath", 'User')
    $env:PATH = "$BinDir;$env:PATH"
    Write-Log "added $BinDir to user PATH"
}

# Register startup via HKCU Run key (no admin, persists across reboots)
$RunKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
Set-ItemProperty -Path $RunKey -Name 'EideticDaemon' -Value "`"$ExePath`""
Write-Log "registered startup key: HKCU\...\Run\EideticDaemon"

# Version smoke-test
$ver = & $ExePath -version 2>&1
Write-Log "smoke: $ver"

# On Windows, daemon listens via TCP (UDS support is limited on Windows)
# Update startup key to include --tcp flag
Set-ItemProperty -Path $RunKey -Name 'EideticDaemon' -Value "`"$ExePath`" --tcp"

# Start daemon now (detached, TCP mode)
$proc = Start-Process -FilePath $ExePath -ArgumentList '--tcp' -WindowStyle Hidden -PassThru
Write-Log "started daemon (TCP mode), pid=$($proc.Id)"
Start-Sleep -Seconds 1

Write-Host ""
Write-Host "Eidetic Works $Version installed." -ForegroundColor Green
Write-Host "  Binary:  $ExePath"
Write-Host "  Mode:    TCP loopback (127.0.0.1:9876)"
Write-Host "  Startup: HKCU Run key (auto-start at login)"
Write-Host ""
Write-Host "MCP bridge (Claude Code / Cursor):"
Write-Host "  pip install eidetic-mcp"
Write-Host "  claude mcp add eidetic -- python -m eidetic_mcp.server"
Write-Host "  (Windows auto-uses TCP — no extra config needed)"
Write-Host ""
Write-Host "Verify: curl http://127.0.0.1:9876/metrics"
