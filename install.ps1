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
#   * Prebuilt downloads PREFER the tree-sitter-enabled -full zip (shipped
#     for windows amd64); set $env:KERN_FORCE_PUREGO="1" to use the pure-Go
#     zip instead (also the automatic fallback when no -full asset exists).
#   * Adds the install dir to the user PATH (persisted via User environment).
#   * Verifies `kern.exe version` and performs an MCP JSON-RPC initialize handshake.
#   * Wires kern ONCE, machine-wide (`kern setup --global-only --global-rules`):
#     user-level MCP + hooks + rules for every agent, NO file written in any
#     repo. A repo initializes itself when an agent first works in it.
#     Set $env:KERN_AUTO_WIRE="0" to install the binaries only.
#   * Supports actions: install (default), upgrade, status, uninstall.
#   * uninstall is prefix-scoped by default (binaries + this prefix's User
#     PATH entry); -Machine (or KERN_UNINSTALL_MACHINE=1) also removes the
#     ~/AGENTS.md kern block, machine secrets (%APPDATA%\kern), and caches.
#   * upgrade re-runs the install path; a running kern.exe/kern-mcp.exe
#     is moved aside (unique .old name) before the copy — Windows cannot
#     overwrite a running binary, but it can rename it. If the copy fails,
#     the aside is restored so the previous binary stays in place.
#
# Distribution note: replace JayveerPrajapati below with your GitHub username
# (or run scripts/retarget.sh which rewrites this file for you).

param(
    [string]$Action = "install",
    [switch]$Machine
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

# --- Output style -----------------------------------------------------------
# Colour via Write-Host -ForegroundColor (NO_COLOR and KERN_PLAIN respected).
# Glyphs and box characters are unicode only where the console renders them
# (Windows Terminal, PowerShell 7+); legacy consoles get ASCII. They are built
# from code points so this file stays pure ASCII (no BOM/encoding problems).
$UseColor = (-not $env:NO_COLOR) -and (-not $env:KERN_PLAIN)
$UseUnicode = (-not $env:KERN_ASCII) -and ($env:WT_SESSION -or ($PSVersionTable.PSVersion.Major -ge 7))
if ($UseUnicode) {
    $GOk = [string][char]0x2714; $GWarn = [string][char]0x26A0
    $BTL = [string][char]0x256D; $BTR = [string][char]0x256E
    $BBL = [string][char]0x2570; $BBR = [string][char]0x256F
    $BH = [string][char]0x2500; $BV = [string][char]0x2502
} else {
    $GOk = "ok"; $GWarn = "!"
    $BTL = "+"; $BTR = "+"; $BBL = "+"; $BBR = "+"; $BH = "-"; $BV = "|"
}
# Was the install dir already on the user PATH before this run changes it?
$PathWasSet = ([Environment]::GetEnvironmentVariable("Path", "User") -like "*$Prefix*")

function Write-Colored {
    param([string]$Text, [string]$Color = "", [switch]$NoNewline)
    if ($UseColor -and $Color) { Write-Host $Text -ForegroundColor $Color -NoNewline:$NoNewline }
    else { Write-Host $Text -NoNewline:$NoNewline }
}
function Write-Step {
    param([string]$Msg)
    Write-Host "  " -NoNewline
    Write-Colored $GOk "Green" -NoNewline
    Write-Host " $Msg"
}
function Write-Warn {
    param([string]$Msg)
    Write-Host "  " -NoNewline
    Write-Colored $GWarn "Yellow" -NoNewline
    Write-Host " $Msg"
}
function Write-Header {
    param([string]$Msg)
    Write-Host ""
    Write-Colored "  $Msg" "Cyan"
}
# Boxes are 62 columns wide; content must be ASCII and at most 58 columns.
function Write-Box {
    param([string]$Title, [string[]]$Lines)
    $n = 57 - $Title.Length
    Write-Colored ("  " + $BTL + $BH + " " + $Title + " " + ($BH * $n) + $BTR) "DarkGray"
    foreach ($l in $Lines) {
        Write-Colored ("  " + $BV + " ") "DarkGray" -NoNewline
        Write-Host $l.PadRight(58) -NoNewline
        Write-Colored (" " + $BV) "DarkGray"
    }
    Write-Colored ("  " + $BBL + ($BH * 60) + $BBR) "DarkGray"
}

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
    param([string]$Tag)
    # F7b (Windows port of the install.sh fix): build from the RESOLVED
    # release tag, never a bare @latest — a dev-HEAD build must not silently
    # replace a release. If the tag cannot be honored, refuse loudly.
    if (-not $Tag) {
        Write-Host "kern: cannot fall back to 'go install' without a resolved release tag — refusing to install an arbitrary @latest build" -ForegroundColor Red
        return $false
    }
    $purego = $env:KERN_PUREGO
    if ($purego -eq "1") {
        Write-Host "kern: KERN_PUREGO=1 — installing the pure-Go build (-tags notreesitter, no C toolchain)" -ForegroundColor Yellow
        go install -tags notreesitter "github.com/$Repo/cmd/kern@$Tag"
        go install -tags notreesitter "github.com/$Repo/cmd/kern-mcp@$Tag"
    } else {
        Write-Host "kern: falling back to 'go install github.com/$Repo/cmd/kern@$Tag'" -ForegroundColor Yellow
        # Default go-install build compiles tree-sitter in (hard CGO). On
        # Windows without a C toolchain this fails; set KERN_PUREGO=1 to use
        # the pure-Go -tags notreesitter build instead.
        go install "github.com/$Repo/cmd/kern@$Tag"
        go install "github.com/$Repo/cmd/kern-mcp@$Tag"
    }
    # go install drops the binaries into $(go env GOPATH)/bin, which is often
    # NOT on PATH and never reaches $Prefix. Copy each binary independently:
    # kern.exe is required, kern-mcp.exe is copied when present. The old
    # all-three check (the third binary was retired in the platform pivot)
    # blocked both copies even when they had built successfully (P0-5).
    $goBin = Join-Path (& go env GOPATH) "bin"
    New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
    $copied = @()
    foreach ($b in @("kern.exe", "kern-mcp.exe")) {
        $src = Join-Path $goBin $b
        if (Test-Path $src) {
            Copy-Item $src (Join-Path $Prefix $b) -Force
            $copied += $b
        }
    }
    $ok = Test-Path (Join-Path $Prefix "kern.exe")
    if ($ok) {
        Write-Host "kern: copied $($copied -join ', ') to $Prefix from $goBin." -ForegroundColor Green
    } else {
        Write-Host "kern: installed via go install, but could not find kern.exe in $goBin."
        Write-Host "kern: ensure `$(go env GOPATH)/bin is on your PATH and run 'kern setup' in your project."
    }
    return $ok
}

function Confirm-Sha256 {
    param([string]$Path, [string]$Expected)
    # Fail-closed (mirrors install.sh's verify contract): a missing expected
    # checksum is as fatal as a mismatch — a Windows user must never receive
    # an unverified binary with zero warning.
    if (-not $Expected) {
        Write-Host "kern: no SHA256SUMS entry for $([IO.Path]::GetFileName($Path)) — refusing to install an unverified binary" -ForegroundColor Red
        return $false
    }
    $hash = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
    if ($hash -ne $Expected) {
        Write-Host "kern: checksum mismatch (expected $Expected, got $hash)" -ForegroundColor Red
        return $false
    }
    Write-Host "kern: checksum ok ($hash)"
    return $true
}

# Show-NextSteps: what to do after an install that wired nothing.
function Show-NextSteps {
    Write-Host ""
    Write-Box "kern is installed" (@(
        "Nothing was wired into your agents or git yet.",
        "",
        "1. kern doctor          check install and index health",
        "2. kern setup --dry-run preview what wiring would write",
        "3. kern setup           wire detected agents (asks first)",
        "",
        "Wire all agents machine-wide: kern setup --global-only"
    ) + $(if (-not $PathWasSet) { @("", "Open a NEW terminal so the updated PATH applies.") } else { @() }))
}

# Show-WiredSummary: what a successful global wiring means.
function Show-WiredSummary {
    Write-Host ""
    Write-Box "kern is wired globally" (@(
        "Every agent now has kern (MCP + hooks), machine-wide.",
        "No repo was touched. A repo initializes itself the first",
        "time an agent works in it (kern_buddy builds the index).",
        "",
        "Restart your agent to load kern.",
        "",
        "kern setup --dry-run preview what wiring would write",
        "kern doctor       check wiring and index health",
        "kern setup        add shared config to a team repo",
        "",
        "Opt out of auto-wiring: KERN_AUTO_WIRE=0 when installing"
    ) + $(if (-not $PathWasSet) { @("", "Open a NEW terminal so the updated PATH applies.") } else { @() }))
}

# Wire-Kern registers kern ONCE, machine-wide: user-level MCP for every agent,
# global hooks, global rules and global git ignore (`kern setup --global-only
# --global-rules`). It writes NO file in any repo and does not index anything:
# a repo initializes itself when an agent first works in it. KERN_AUTO_WIRE=0
# installs the binaries only. An upgrade refreshes the same user-level assets.
function Wire-Kern {
    $oldPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($oldPath -notlike "*$Prefix*") {
        [Environment]::SetEnvironmentVariable("Path", "$oldPath;$Prefix", "User")
        Write-Warn "added $Prefix to your user PATH (open a new terminal)."
    }
    $kern = Join-Path $Prefix "kern.exe"
    if (-not (Test-Path $kern)) {
        Write-Host "kern: binary not found at $kern; skipping auto-wire." -ForegroundColor Red
        Write-Host "  run 'kern setup --global-only' manually."
        return
    }
    if ($env:KERN_AUTO_WIRE -eq "0") {
        Show-NextSteps
        return
    }
    Write-Header "Wiring kern for every agent (user-level, no repo files)"
    $wired = $false
    try {
        & $kern setup --global-only --global-rules --yes
        $wired = ($LASTEXITCODE -eq 0)
    } catch { $wired = $false }
    if ($wired) {
        Write-Step "global wiring complete - 'kern setup --check' shows current wiring"
        Show-WiredSummary
    } else {
        Write-Warn "global wiring failed - run 'kern setup --global-only' to see why"
        Show-NextSteps
    }
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

# Remove-KernSection excises EXACTLY the kern-managed block(s) from a
# rules file, preserving every non-kern byte — the PowerShell port of
# install.sh's remove_kern_section. Handles both formats kern setup writes:
#   - the marker format ("<!-- kern:global-rules begin ... -->" through its
#     matching "<!-- kern:global-rules end -->" line), and
#   - the unmarked format ("# kern usage rules" heading through the next H1,
#     anchored on the rules asset's own tail line so trailing user content
#     without an H1 is never swallowed).
# Throws on an unbalanced marker block so the caller can abort rather than
# destroy the file.
function Remove-KernSection {
    param([string]$Content)
    # Split on LF with an optional preceding CR so Windows CRLF files match
    # the same anchors as unix LF files.
    $lines = $Content -split "\r?\n"
    $out = [System.Collections.Generic.List[string]]::new()
    $buf = [System.Collections.Generic.List[string]]::new()
    $mode = ""          # "", "marker" or "unmarked"
    $anchored = $false
    foreach ($line in $lines) {
        if ($mode -eq "" -and ($line -cmatch '^# kern usage rules' -or $line -cmatch '^<!-- kern:global-rules begin')) {
            $mode = if ($line -cmatch '^<!-- kern:global-rules begin') { "marker" } else { "unmarked" }
            $anchored = $false
            $buf.Clear()
            continue
        }
        if ($mode -eq "marker") {
            if ($line -cmatch '^<!-- kern:global-rules end -->') { $mode = "" }
            continue
        }
        if ($mode -eq "unmarked") {
            if ($line -cmatch '^# ') { $mode = ""; $out.Add($line); continue }
            if ($line -cmatch '^<!-- kern:global-omit:end -->$' -or $line -cmatch '^\(`KERN_MCP_FULL=1` only\)\.$') {
                $anchored = $true
                $buf.Clear()
                continue
            }
            $buf.Add($line)
            continue
        }
        $out.Add($line)
    }
    if ($mode -eq "marker") {
        throw "unbalanced kern-managed marker block (no closing '<!-- kern:global-rules end -->')"
    }
    if ($mode -eq "unmarked" -and $anchored) {
        $skipBlank = $true
        foreach ($l in $buf) {
            if ($skipBlank -and $l -match '^[ \t]*$') { continue }
            $skipBlank = $false
            $out.Add($l)
        }
    }
    return ($out -join "`n")
}

function Uninstall-Kern {
    # Env fallback mirrors install.sh (KERN_UNINSTALL_MACHINE=1), so
    # scripted uninstalls and test harnesses can opt in without the switch.
    if ($env:KERN_UNINSTALL_MACHINE -eq "1") { $Machine = $true }
    Write-Host "kern: uninstalling from $Prefix..."
    foreach ($b in @("kern.exe", "kern-mcp.exe")) {
        $p = Join-Path $Prefix $b
        if (Test-Path $p) {
            Remove-Item -Force $p -ErrorAction SilentlyContinue
            Write-Host "  removed $p"
        }
    }
    if (-not $Machine) {
        # F4 (2026-10-28): a default uninstall is prefix-scoped — machine
        # state belonging to another install must survive a scratch-prefix
        # uninstall. Mirrors install.sh's scoped mode.
        Write-Host "  (prefix-scoped: this prefix's User PATH entry only — pass -Machine to also"
        Write-Host "   remove ~/AGENTS.md, machine secrets, and caches)"
    } else {
    # Machine secrets live under os.UserConfigDir() (%APPDATA%\kern on
    # Windows): audit-chain key, approval signing key/HMAC, consumed ledger.
    # The cache dir (%LOCALAPPDATA%\kern) is removed with it.
    foreach ($d in @((Join-Path $env:APPDATA "kern"), (Join-Path $env:LOCALAPPDATA "kern"))) {
        if ($d -and (Test-Path $d)) {
            Remove-Item -Recurse -Force $d -ErrorAction SilentlyContinue
            Write-Host "  removed $d (machine secrets)"
        }
    }
    }
    $oldPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($oldPath -like "*$Prefix*") {
        $newPath = (($oldPath -split ";" | Where-Object { $_ -ne $Prefix -and $_.Trim() -ne "" }) -join ";")
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host "  removed $Prefix from User PATH"
    }
    if ($Machine) {
    # ~/AGENTS.md: back up first, then excise EXACTLY the kern-managed block
    # (both formats), preserving every non-kern byte. Messages are honest:
    # the file is only deleted when it contained nothing but the kern block,
    # and the backup path is always reported (mirrors the fixed install.sh).
    $agents = Join-Path $HOME "AGENTS.md"
    if (Test-Path $agents) {
        $content = Get-Content -Raw -Path $agents
        if ($content -cmatch '(?m)^# kern usage rules' -or $content -cmatch '(?m)^<!-- kern:global-rules begin') {
            $bak = "$agents.kern-uninstall.bak"
            Copy-Item -Force -Path $agents -Destination $bak
            Write-Host "  backed up $agents to $bak"
            try {
                $cleaned = Remove-KernSection -Content $content
            } catch {
                Write-Host "kern: $($_.Exception.Message) — $agents left untouched (backup at $bak)" -ForegroundColor Red
                exit 1
            }
            if ([string]::IsNullOrWhiteSpace($cleaned)) {
                Remove-Item -Force $agents
                Write-Host "  removed $agents (it contained only the kern-managed block; backup at $bak)"
            } else {
                Set-Content -NoNewline -Path $agents -Value $cleaned
                Write-Host "  removed kern-managed block from $agents (other content preserved; backup at $bak)"
            }
        } else {
            Write-Host "  no kern-managed block in $agents — left untouched"
        }
    } else {
        Write-Host "  no $agents — nothing to remove"
    }
    # ~/.config/kern and ~/.cache/kern, when present (mirrors install.sh).
    foreach ($d in @((Join-Path $HOME ".config\kern"), (Join-Path $HOME ".cache\kern"))) {
        if (Test-Path $d) {
            Remove-Item -Recurse -Force $d
            Write-Host "  removed $d"
        }
    }
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

Write-Header "kern installer ($Action)"
$tag = Get-Version
if (-not $tag) {
    # F7b: an unresolvable release means the tag cannot be honored — abort
    # loudly instead of installing an unverified @latest build.
    Write-Host "kern: could not resolve a release version — refusing to install an unverified @latest build; check your network or pin KERN_VERSION" -ForegroundColor Red
    exit 1
}

# Detect architecture (Windows prebuilt = amd64 and arm64).
$arch = $env:PROCESSOR_ARCHITECTURE
$goarch = if ($arch -match "ARM64") { "arm64" } elseif ($arch -match "64") { "amd64" } else { "386" }
if ($goarch -ne "amd64" -and $goarch -ne "arm64") {
    Write-Host "kern: no prebuilt asset for $goarch on Windows; falling back to go install." -ForegroundColor Yellow
    if (Get-Command go -ErrorAction SilentlyContinue) {
        $goInstalled = Install-Go -Tag $tag
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
    # Prefer the tree-sitter-enabled native build: probe for the -full zip
    # FIRST (windows amd64 ships one). A clean 404 falls back to the pure-Go
    # zip; any other probe failure also falls back with a warning. The
    # selected zip is checksum-verified below, so either asset is checked the
    # same way. KERN_FORCE_PUREGO=1 skips the probe entirely.
    $zip = $null
    $forcePureGo = $env:KERN_FORCE_PUREGO
    if ($forcePureGo -eq "1") {
        Write-Host "kern: KERN_FORCE_PUREGO=1 — using the pure-Go build ($file)" -ForegroundColor Yellow
    } else {
        $fullFile = "kern-windows-${goarch}-full.zip"
        $fullZip = Join-Path $tmp $fullFile
        try {
            Invoke-WebRequest -Uri "https://github.com/$Repo/releases/download/$tag/$fullFile" -OutFile $fullZip -UseBasicParsing
            $file = $fullFile
            $url = "https://github.com/$Repo/releases/download/$tag/$file"
            $zip = $fullZip
            Write-Host "kern: using tree-sitter build ($file)" -ForegroundColor Green
        } catch {
            $status = $null
            if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
            if ($status -eq 404) {
                Write-Host "kern: no -full (tree-sitter) asset for Windows at $tag; using the pure-Go build ($file)" -ForegroundColor Yellow
            } else {
                Write-Host "kern: could not probe $fullFile (HTTP $status) — using the pure-Go build ($file)" -ForegroundColor Yellow
            }
        }
    }
    if (-not $zip) { $zip = Join-Path $tmp $file }
    if (-not (Test-Path $zip)) {
        Write-Host "kern: downloading $tag ($file)"
        try {
            Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing
        } catch {
            Write-Host "kern: no prebuilt asset for Windows at $tag; falling back to go install." -ForegroundColor Yellow
            if (Get-Command go -ErrorAction SilentlyContinue) {
                $goInstalled = Install-Go -Tag $tag
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
    }

    # Fail-closed checksum verification against the release SHA256SUMS
    # asset (mirrors install.sh's verify): a missing asset/entry or a
    # mismatch aborts the install — never an unverified binary.
    $sumsUrl = "https://github.com/$Repo/releases/download/$tag/SHA256SUMS"
    $sumsFile = Join-Path $tmp "SHA256SUMS"
    try {
        Invoke-WebRequest -Uri $sumsUrl -OutFile $sumsFile -UseBasicParsing
        $expected = $null
        foreach ($line in (Get-Content -Path $sumsFile)) {
            if ($line -match [regex]::Escape($file)) {
                $expected = ($line.Trim() -split '\s+')[0]
                break
            }
        }
        if (-not (Confirm-Sha256 -Path $zip -Expected $expected)) { exit 1 }
    } catch {
        Write-Host "kern: no SHA256SUMS asset for $tag — refusing to install an unverified binary (build from source with 'go install github.com/$Repo/cmd/kern@$tag', or pick a release that ships checksums)" -ForegroundColor Red
        exit 1
    }

    New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    # The release zip ships the .exe files at the archive root (make dist);
    # search recursively so older layouts (a wrapper dir) also work.
    $extract = Get-ChildItem -Path $tmp -Recurse -Filter "kern.exe" | Select-Object -First 1
    if (-not $extract) { throw "kern.exe not found in archive" }
    Copy-Binary -Source $extract.FullName -Dest (Join-Path $Prefix "kern.exe")

    # kern-mcp.exe is co-located with kern.exe; copy it independently when
    # present. Copy-Binary renames a running binary aside (kern update path).
    $mcp = Get-ChildItem -Path $tmp -Recurse -Filter "kern-mcp.exe" | Select-Object -First 1
    if ($mcp) { Copy-Binary -Source $mcp.FullName -Dest (Join-Path $Prefix "kern-mcp.exe") }

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
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}