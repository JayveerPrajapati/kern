# install.ps1 — kern installer for Windows (PowerShell).
#
#   powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.ps1 | iex"
#
# Behavior (mirrors install.sh):
#   * Defaults to the latest release; pin with $env:KERN_VERSION="v1.2.3".
#   * Installs to $HOME\.local\bin by default, or $env:KERN_INSTALL_DIR if set.
#   * Downloads the prebuilt kern-windows-amd64.zip; falls back to `go install`
#     if the download fails but `go` is present.
#   * Adds the install dir to the current process PATH (persist with setx).
#
# Distribution note: replace JayveerPrajapati below with your GitHub username
# (or run scripts/retarget.sh which rewrites this file for you).

param()

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

function Install-Go {
    Write-Host "kern: falling back to 'go install github.com/$Repo/cmd/kern@$Version'" -ForegroundColor Yellow
    go install "github.com/$Repo/cmd/kern@$Version"
    go install "github.com/$Repo/cmd/kern-mcp@$Version"
    # go install drops both binaries into $(go env GOPATH)/bin, which is often
    # NOT on PATH and never reaches $Prefix. Copy them to the canonical install
    # dir so the PATH step and auto-wire below see them exactly like a prebuilt
    # install. Mirrors install.sh's go_install behaviour.
    $goBin = Join-Path (& go env GOPATH) "bin"
    $ok = $false
    if ((Test-Path (Join-Path $goBin "kern.exe")) -and (Test-Path (Join-Path $goBin "kern-mcp.exe"))) {
        New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
        Copy-Item (Join-Path $goBin "kern.exe") (Join-Path $Prefix "kern.exe") -Force
        Copy-Item (Join-Path $goBin "kern-mcp.exe") (Join-Path $Prefix "kern-mcp.exe") -Force
        $ok = $true
        Write-Host "kern: copied binaries to $Prefix from $goBin." -ForegroundColor Green
    }
    if (-not $ok) {
        Write-Host "kern: installed via go install, but could not find kern.exe/kern-mcp.exe in $goBin."
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

# Wire-Kern adds $Prefix to the user PATH (if needed) and auto-wires kern into
# the detected agents (setup --detect --global) plus auto-indexes the project.
# Extracted into a function so BOTH the prebuilt-install path and the go-install
# fallback run it — a fallback install must wire agents exactly like a prebuilt
# one (previously it returned early and left nothing wired / MCP broken).
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

$tag = Get-Version
if (-not $tag) {
    Write-Host "kern: could not resolve release version" -ForegroundColor Yellow
    if (Get-Command go -ErrorAction SilentlyContinue) { $goInstalled = Install-Go; if ($goInstalled) { & Wire-Kern; exit 0 } ; exit 1 }
    Write-Host "kern: install Go (https://go.dev/dl/) or download from https://github.com/$Repo/releases" -ForegroundColor Red
    exit 1
}

# Detect architecture (Windows prebuilt = amd64 only).
$arch = $env:PROCESSOR_ARCHITECTURE
$goarch = if ($arch -match "ARM64") { "arm64" } elseif ($arch -match "64") { "amd64" } else { "386" }
if ($goarch -ne "amd64") {
    Write-Host "kern: no prebuilt asset for $goarch on Windows; falling back to go install." -ForegroundColor Yellow
    if (Get-Command go -ErrorAction SilentlyContinue) { $goInstalled = Install-Go; if ($goInstalled) { & Wire-Kern; exit 0 }; exit 1 }
    Write-Host "kern: install Go or use a 64-bit Windows." -ForegroundColor Red
    exit 1
}

$file = "kern-windows-amd64.zip"
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
        if (Get-Command go -ErrorAction SilentlyContinue) { $goInstalled = Install-Go; if ($goInstalled) { & Wire-Kern; exit 0 }; exit 1 }
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
    # The zip extracts a directory named kern-windows-amd64/ containing kern.exe.
    $extract = Get-ChildItem -Path $tmp -Recurse -Filter "kern.exe" | Select-Object -First 1
    if (-not $extract) { throw "kern.exe not found in archive" }
    Copy-Item $extract.FullName (Join-Path $Prefix "kern.exe") -Force

    # kern-mcp.exe is co-located; copy if present.
    $mcp = Get-ChildItem -Path $tmp -Recurse -Filter "kern-mcp.exe" | Select-Object -First 1
    if ($mcp) { Copy-Item $mcp.FullName (Join-Path $Prefix "kern-mcp.exe") -Force }

    Write-Host "installed: $(Join-Path $Prefix 'kern.exe') ($tag)"
    & Wire-Kern

    Write-Host "kern is ready. Run 'kern buddy' for a project onboarding digest."
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}