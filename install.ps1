# install.ps1 — kern installer for Windows (PowerShell).
#
#   powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.ps1 | iex"
#
# Behavior (mirrors install.sh):
#   * Defaults to the latest release; pin with $env:KERN_VERSION="v0.9.9.1".
#   * Installs to $HOME\.local\bin by default, or $env:KERN_INSTALL_DIR if set.
#   * Downloads the prebuilt kern-windows-amd64.zip; falls back to `go install`
#     if the download fails but `go` is present.
#   * The go-install fallback builds the default tree-sitter build (hard CGO);
#     set $env:KERN_PUREGO="1" to build the pure-Go opt-out instead
#     (`go install -tags notreesitter ...`, no C toolchain required).
#   * Adds the install dir to the user PATH (persisted via User environment).
#   * Verifies `kern.exe version` and performs an MCP JSON-RPC initialize handshake.
#   * Auto-wires kern into detected agents (`kern setup --detect --global`).
#   * Supports actions: install (default), upgrade, status, uninstall.
#   * upgrade re-runs the install path; a running kern.exe/kern-mcp.exe
#     is moved aside (unique .old name) before the copy — Windows cannot
#     overwrite a running binary, but it can rename it. If the copy fails,
#     the aside is restored so the previous binary stays in place.
#
# Distribution note: replace JayveerPrajapati below with your GitHub username
# (or run scripts/retarget.sh which rewrites this file for you).

param(
    [string]$Action = "install"
)

$ErrorActionPreference = "Stop"
$Owner = $env:KERN_REPO_OWNER
if (-not $Owner) { $Owner = "JayveerPrajapati" }
$Repo = $env:KERN_REPO
if (-not $Repo) { $Repo = "$Owner/kern" }
$Version = $env:KERN_VERSION
if (-not $Version) { $Version = "latest" }
$Prefix = $env:KERN_INSTALL_DIR
if (-not $Prefix) { $Prefix = Join-Path $HOME ".local\bin" }

function Get-Version {
  if ($Version -ne "latest") { return $Version }
  $headers = @{ "Accept" = "application/vnd.github+json"; "User-Agent" = "kern-installer" }
  try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
    return $rel.tag_name
  } catch {
    return $null
  }
}

function Probe-Mcp {
    param([string]$McpPath)
    if (-not (Test-Path $McpPath)) { return $false }
    try {
        $initReq = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kern-installer","version":"1.0"}}}'
        $resp = $initReq | & $McpPath 2>$null | Select-Object -First 1
        if ($resp -and $resp -match '"jsonrpc"') {
            Write-Host "kern: kern-mcp MCP handshake OK" -ForegroundColor Green
            return $true
        }
    } catch {}
    Write-Host "kern: warning: kern-mcp did not answer the MCP initialize handshake" -ForegroundColor Yellow
    return $false
}

function Verify-Kern {
    param([string]$PrefixDir)
    $kern = Join-Path $PrefixDir "kern.exe"
    $mcp = Join-Path $PrefixDir "kern-mcp.exe"
    if (Test-Path $kern) {
        try {
            $ver = (& $kern version 2>$null | Select-Object -First 1)
            if ($ver -match "kern") {
                Write-Host "kern: verified $ver" -ForegroundColor Green
            }
        } catch {}
    }
    if (Test-Path $mcp) {
        Probe-Mcp -McpPath $mcp | Out-Null
    }
}

function Install-Go {
    $purego = $env:KERN_PUREGO
    if ($purego -eq "1") {
        Write-Host "kern: KERN_PUREGO=1 — installing the pure-Go build (-tags notreesitter, no C toolchain)" -ForegroundColor Yellow
        go install -tags notreesitter "github.com/$Repo/cmd/kern@$Version"
        go install -tags notreesitter "github.com/$Repo/cmd/kern-mcp@$Version"
        go install -tags notreesitter "github.com/$Repo/cmd/kern-server@$Version"
    } else {
        Write-Host "kern: falling back to 'go install github.com/$Repo/cmd/kern@$Version'" -ForegroundColor Yellow
        # Default go-install build compiles tree-sitter in (hard CGO). On
        # Windows without a C toolchain this fails; set KERN_PUREGO=1 to use
        # the pure-Go -tags notreesitter build instead.
        go install "github.com/$Repo/cmd/kern@$Version"
        go install "github.com/$Repo/cmd/kern-mcp@$Version"
        go install "github.com/$Repo/cmd/kern-server@$Version"
    }
    # go install drops all three binaries into $(go env GOPATH)/bin, which is
    # often NOT on PATH and never reaches $Prefix. Copy them to the canonical
    # install dir so the PATH step and auto-wire below see them exactly like a
    # prebuilt install. Mirrors install.sh's go_install behaviour.
    $goBin = Join-Path (& go env GOPATH) "bin"
    $ok = $false
    if ((Test-Path (Join-Path $goBin "kern.exe")) -and (Test-Path (Join-Path $goBin "kern-mcp.exe")) -and (Test-Path (Join-Path $goBin "kern-server.exe"))) {
        New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
        Copy-Item (Join-Path $goBin "kern.exe") (Join-Path $Prefix "kern.exe") -Force
        Copy-Item (Join-Path $goBin "kern-mcp.exe") (Join-Path $Prefix "kern-mcp.exe") -Force
        Copy-Item (Join-Path $goBin "kern-server.exe") (Join-Path $Prefix "kern-server.exe") -Force
        $ok = $true
        Write-Host "kern: copied binaries to $Prefix from $goBin." -ForegroundColor Green
    }
    if (-not $ok) {
        Write-Host "kern: installed via go install, but could not find kern.exe/kern-mcp.exe/kern-server.exe in $goBin."
        Write-Host "kern: ensure `$(go env GOPATH)/bin is on your PATH and run 'kern setup' in your project."
    }
    return $ok
}

function Confirm-Sha256 {
    param([string]$Path, [string]$Expected)
    if (-not $Expected) { return $true }
    $hash = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
    if ($hash -ne $Expected) {
        Write-Host "kern: checksum mismatch (expected $Expected, got $hash)" -ForegroundColor Red
        return $false
    }
    Write-Host "kern: checksum ok ($hash)"
    return $true
}

function Wire-Kern {
    $oldPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($oldPath -notlike "*$Prefix*") {
        [Environment]::SetEnvironmentVariable("Path", "$oldPath;$Prefix", "User")
        Write-Host "note: added $Prefix to your user PATH (open a new terminal)." -ForegroundColor Yellow
    }
    $kern = Join-Path $Prefix "kern.exe"
    if (-not (Test-Path $kern)) {
        Write-Host "kern: binary not found at $kern; skipping auto-wire." -ForegroundColor Red
        Write-Host "  run 'kern setup --detect --global' manually in your project root."
        return
    }
    try {
        $projRoot = git rev-parse --show-toplevel 2>$null
        if (-not $projRoot) { $projRoot = (Get-Location).Path }
        Write-Host "auto-wiring kern into detected agents in: $projRoot (and globally)"
        Push-Location $projRoot
        try {
            & $kern setup --detect --root $projRoot --global
            Write-Host "indexing project (first run may take a minute)..."
            & $kern index $projRoot 2>$null
        } finally {
            Pop-Location
        }
    } catch { <# non-fatal: setup/index is best-effort #> }
}

function Show-Status {
    $arch = $env:PROCESSOR_ARCHITECTURE
    Write-Host "kern installer status"
    Write-Host "  platform:    windows-$arch"
    Write-Host "  install dir: $Prefix"
    $kern = Join-Path $Prefix "kern.exe"
    if (Test-Path $kern) {
        $ver = (& $kern version 2>$null | Select-Object -First 1)
        Write-Host "  installed:   $ver ($kern)"
        $mcp = Join-Path $Prefix "kern-mcp.exe"
        if (Test-Path $mcp) {
            Probe-Mcp -McpPath $mcp | Out-Null
        }
    } else {
        Write-Host "  installed:   not installed in $Prefix"
    }
}

function Uninstall-Kern {
    Write-Host "kern: uninstalling from $Prefix..."
    foreach ($b in @("kern.exe", "kern-mcp.exe", "kern-server.exe")) {
        $p = Join-Path $Prefix $b
        if (Test-Path $p) {
            Remove-Item -Force $p -ErrorAction SilentlyContinue
            Write-Host "  removed $p"
        }
    }
    $oldPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($oldPath -like "*$Prefix*") {
        $newPath = (($oldPath -split ";" | Where-Object { $_ -ne $Prefix -and $_.Trim() -ne "" }) -join ";")
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host "  removed $Prefix from User PATH"
    }
    Write-Host "kern: uninstalled successfully." -ForegroundColor Green
}

function Copy-Binary {
    param([string]$Source, [string]$Dest)
    # A running .exe cannot be overwritten on Windows, but it CAN be
    # renamed: move the current binary aside so `kern update` (which runs
    # this installer while kern.exe is still running) works. The aside
    # name is unique per invocation, so a stale .old locked by an old
    # process can never block the move. If the copy then fails, the aside
    # is moved back so the previous binary stays in place (no bricked
    # install). An aside left locked by the still-running old process is
    # cleaned up by the next install.
    if (Test-Path $Dest) {
        $aside = "$Dest.$([guid]::NewGuid().ToString('N')).old"
        Move-Item -Force $Dest $aside -ErrorAction Stop
    }
    try {
        Copy-Item $Source $Dest -Force
    } catch {
        if (Test-Path $aside) { Move-Item -Force $aside $Dest -ErrorAction Stop }
        throw
    }
}

# Command dispatch: install and upgrade both run the install path below
# (only status/uninstall are special-cased), so `kern update` can pass
# -Action upgrade and land here.
if ($Action -eq "status" -or ($args.Count -gt 0 -and $args[0] -eq "status")) {
    Show-Status
    exit 0
}
if ($Action -eq "uninstall" -or ($args.Count -gt 0 -and $args[0] -eq "uninstall")) {
    Uninstall-Kern
    exit 0
}

$tag = Get-Version
if (-not $tag) {
    Write-Host "kern: could not resolve release version" -ForegroundColor Yellow
    if (Get-Command go -ErrorAction SilentlyContinue) {
        $goInstalled = Install-Go
        if ($goInstalled) {
            Verify-Kern -PrefixDir $Prefix
            & Wire-Kern
            exit 0
        }
        exit 1
    }
    Write-Host "kern: install Go (https://go.dev/dl/) or download from https://github.com/$Repo/releases" -ForegroundColor Red
    exit 1
}

# Detect architecture (Windows prebuilt = amd64 and arm64).
$arch = $env:PROCESSOR_ARCHITECTURE
$goarch = if ($arch -match "ARM64") { "arm64" } elseif ($arch -match "64") { "amd64" } else { "386" }
if ($goarch -ne "amd64" -and $goarch -ne "arm64") {
    Write-Host "kern: no prebuilt asset for $goarch on Windows; falling back to go install." -ForegroundColor Yellow
    if (Get-Command go -ErrorAction SilentlyContinue) {
        $goInstalled = Install-Go
        if ($goInstalled) {
            Verify-Kern -PrefixDir $Prefix
            & Wire-Kern
            exit 0
        }
        exit 1
    }
    Write-Host "kern: install Go or use a 64-bit Windows." -ForegroundColor Red
    exit 1
}

$file = "kern-windows-${goarch}.zip"
$url = "https://github.com/$Repo/releases/download/$tag/$file"
$tmp = Join-Path $env:TEMP "kern-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

try {
    Write-Host "kern: downloading $tag ($file)"
    $zip = Join-Path $tmp $file
    try {
        Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
    } catch {
        Write-Host "kern: no prebuilt asset for Windows at $tag; falling back to go install." -ForegroundColor Yellow
        if (Get-Command go -ErrorAction SilentlyContinue) {
            $goInstalled = Install-Go
            if ($goInstalled) {
                Verify-Kern -PrefixDir $Prefix
                & Wire-Kern
                exit 0
            }
            exit 1
        }
        Write-Host "kern: download failed and go is not installed." -ForegroundColor Red
        exit 1
    }

    # Best-effort checksum verification against the release SHA256SUMS asset.
    $sumsUrl = "https://github.com/$Repo/releases/download/$tag/SHA256SUMS"
    try {
        $sums = Invoke-WebRequest -Uri $sumsUrl -UseBasicParsing
        $expected = ($sums.Content -split "`n" | Where-Object { $_ -match [regex]::Escape($file) }) -split "\s+" | Select-Object -First 1
        if ($expected) { if (-not (Confirm-Sha256 -Path $zip -Expected $expected)) { exit 1 } }
    } catch { <# SHA256SUMS unavailable; skip #> }

    New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    # The release zip ships the .exe files at the archive root (make dist);
    # search recursively so older layouts (a wrapper dir) also work.
    $extract = Get-ChildItem -Path $tmp -Recurse -Filter "kern.exe" | Select-Object -First 1
    if (-not $extract) { throw "kern.exe not found in archive" }
    Copy-Binary -Source $extract.FullName -Dest (Join-Path $Prefix "kern.exe")

    # kern-mcp.exe and kern-server.exe are co-located; copy if present.
    # Copy-Binary renames a running binary aside (kern update path).
    $mcp = Get-ChildItem -Path $tmp -Recurse -Filter "kern-mcp.exe" | Select-Object -First 1
    if ($mcp) { Copy-Binary -Source $mcp.FullName -Dest (Join-Path $Prefix "kern-mcp.exe") }
    $server = Get-ChildItem -Path $tmp -Recurse -Filter "kern-server.exe" | Select-Object -First 1
    if ($server) { Copy-Binary -Source $server.FullName -Dest (Join-Path $Prefix "kern-server.exe") }

    # Best-effort cleanup of binaries renamed aside by a previous update.
    # (A .old moved aside by THIS update may still be locked by the old
    # running process until it exits; the next install removes it.)
    # Only kern-owned aside files are removed (legacy <name>.exe.old and
    # the unique <name>.exe.<guid>.old form); other tools sharing $Prefix
    # are never touched.
    Get-ChildItem -Path $Prefix -Filter "kern*" -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '^kern(\.exe|\.exe\.[0-9a-f]{32})\.old$' } |
        Remove-Item -Force -ErrorAction SilentlyContinue

    Write-Host "installed: $(Join-Path $Prefix 'kern.exe') ($tag)"
    Verify-Kern -PrefixDir $Prefix
    & Wire-Kern

    Write-Host "kern is ready. Run 'kern buddy' for a project onboarding digest."
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}