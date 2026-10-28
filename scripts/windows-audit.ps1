#requires -version 5.1
<#
  kern Windows behavior audit — self-contained, non-destructive.
  Run in PowerShell (5.1 or 7):
      powershell -ExecutionPolicy Bypass -File .\windows-audit.ps1
  or with an existing checkout:
      .\windows-audit.ps1 -RepoPath C:\path\to\kern

  Everything (HOME/USERPROFILE/APPDATA/GOPATH/GOCACHE/GIT_CONFIG_GLOBAL)
  is redirected under %TEMP%\kern-win-audit-<id>, so the real profile is
  never touched. No admin rights required. The script builds kern from
  source (pure-Go, no C toolchain) and prints PASS/FAIL evidence for the
  Windows findings in the 2026-10-28 adversarial report.
#>
param([string]$RepoPath = "")

$ErrorActionPreference = "Continue"
$ProgressPreference = "SilentlyContinue"
$root = Join-Path $env:TEMP ("kern-win-audit-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $root | Out-Null
$results = New-Object System.Collections.Generic.List[object]
function Record([string]$name, $value) {
    $results.Add([pscustomobject]@{ Check = $name; Result = [string]$value })
    Write-Host ("[{0}] {1}" -f $name, $value)
}
function WriteText([string]$path, [string]$text) {
    [IO.File]::WriteAllText($path, $text)
}
function First-Lines($text, [int]$n) {
    $lines = ($text -split "`r?`n")
    if ($lines.Count -gt $n) { return (($lines[0..($n-1)]) -join "`n") + "`n..." }
    return ($lines -join "`n")
}

# ---------- isolated environment (no real profile writes) ----------
$env:USERPROFILE   = $root
$env:HOME          = $root
$env:APPDATA       = Join-Path $root "AppData\Roaming"
$env:LOCALAPPDATA  = Join-Path $root "AppData\Local"
$env:XDG_CONFIG_HOME = Join-Path $root ".config"
$env:GIT_CONFIG_GLOBAL = Join-Path $root ".gitconfig"
$env:GOPATH        = Join-Path $root "gopath"
$env:GOCACHE       = Join-Path $root "gocache"
$env:GOMODCACHE    = Join-Path $root "gomodcache"
$env:KERN_DAEMON   = "0"
foreach ($d in @($env:APPDATA, $env:LOCALAPPDATA, $env:XDG_CONFIG_HOME,
                 $env:GOPATH, $env:GOCACHE, $env:GOMODCACHE)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}
Write-Host "audit root: $root"
Write-Host "ps: $($PSVersionTable.PSVersion) | go: $(try { (& go version) } catch { 'missing' })"
Write-Host "git: $(try { (& git --version) } catch { 'missing' })"

# ---------- 1. build ----------
if ($RepoPath) {
    $src = (Resolve-Path $RepoPath).Path
} else {
    $src = Join-Path $root "src"
    & git clone --depth 1 https://github.com/JayveerPrajapati/kern $src 2>&1 | Write-Host
}
$kern = Join-Path $root "kern.exe"
$mcp  = Join-Path $root "kern-mcp.exe"
$env:CGO_ENABLED = "0"
Push-Location $src
& go build -tags "sqlite,notreesitter" -o $kern .\cmd\kern 2>&1 | Write-Host
& go build -tags "sqlite,notreesitter" -o $mcp  .\cmd\kern-mcp 2>&1 | Write-Host
Pop-Location
Record "01-build" ("kern=" + (Test-Path $kern) + " mcp=" + (Test-Path $mcp))
if (-not (Test-Path $kern)) {
    Record "FATAL" "build failed - see output above"
    Write-Host "root: $root"; exit 1
}
$ver = (& $kern version 2>&1) -join " "
Record "02-version" $ver

# ---------- 2. Windows classifier matrix (pure Go; executes nothing) ----------
$winCmds = @(
    "del /f /q x", "del x", "rmdir /s /q d", "rd /s /q d",
    "Remove-Item -Recurse -Force d",
    "powershell -Command ""Remove-Item -Recurse -Force d""",
    "cmd /c ""del /f x""", "cmd /c ""rmdir /s /q d""",
    "Invoke-WebRequest http://example.com -OutFile y",
    "format c:", "diskpart", "reg delete HKLM\Software\x /f",
    "sc stop x", "net user x y /add", "takeown /f x",
    "icacls x /grant Everyone:F"
)
$gated = 0
foreach ($c in $winCmds) {
    $raw = & $kern guard classify $c --json 2>$null
    try { $v = ($raw | ConvertFrom-Json).verdict } catch { $v = "ERR" }
    if ($v -eq "gate") { $gated++ }
    Write-Host ("    {0,-58} {1}" -f $c, $v)
}
Record "03-windows-classifier" ("$gated/$($winCmds.Count) gate (expected all; 'governed' = no approval)")

# ---------- 3. project: index, native verify, command verify ----------
$proj = Join-Path $root "proj"
New-Item -ItemType Directory -Force -Path $proj | Out-Null
WriteText (Join-Path $proj "go.mod")  "module ex.com/p`n`ngo 1.27`n"
WriteText (Join-Path $proj "p.go")    "package p`n`nfunc Add(a, b int) int { return a + b }`n"
WriteText (Join-Path $proj "p_test.go") "package p`n`nimport ""testing""`n`nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatalf(""bad"") } }`n"
Push-Location $proj
$idx = (& $kern index . 2>&1) -join "`n"
Write-Host (First-Lines $idx 3)
Record "04-index" ("exit=$LASTEXITCODE")
$bt = (& $kern verify build,test 2>&1) -join "`n"
Write-Host (First-Lines $bt 6)
Record "05-verify-build-test" ("exit=$LASTEXITCODE")
$vc = (& $kern verify --command "go test ./..." --output summary 2>&1) -join "`n"
Write-Host (First-Lines $vc 6)
Record "06-verify-command" ("exit=$LASTEXITCODE (0 = sh works / non-zero = sh missing or other)")
Pop-Location

# ---------- 4. governance execution (safe scratch victims; opt-in exec posture) ----------
$env:KERN_ALLOW_EXEC = "1"
Push-Location $proj
WriteText (Join-Path $proj "victim.txt") "x"
$del = (& $kern build "del /f /q victim.txt" 2>&1) -join "`n"
Record "07-build-del" ("exit=$LASTEXITCODE victim_exists=" + (Test-Path (Join-Path $proj "victim.txt")) + " | " + (First-Lines $del 1))
New-Item -ItemType Directory -Force -Path (Join-Path $proj "d") | Out-Null
$rd = (& $kern build "rmdir /s /q d" 2>&1) -join "`n"
Record "08-build-rmdir" ("exit=$LASTEXITCODE dir_exists=" + (Test-Path (Join-Path $proj "d")) + " | " + (First-Lines $rd 1))
Pop-Location

# ---------- 5. secret scanner blind spot ----------
$key = "-----BEGIN RSA PRIVATE KEY-----`nMIIEowIBAAKCAQEA1234567890abcdef`n-----END RSA PRIVATE KEY-----`n"
WriteText (Join-Path $proj "leak.pem") $key
WriteText (Join-Path $proj "leak.env") $key
Push-Location $proj
$sec = (& $kern sec . 2>&1) -join "`n"
Write-Host (First-Lines $sec 4)
Record "09-sec-detects-env" ($sec -match "leak\.env")
Record "10-sec-detects-pem" ($sec -match "leak\.pem")
Pop-Location

# ---------- 6. verify --staged exit code on a critical finding ----------
Push-Location $proj
& git init -q 2>$null
& git config user.email audit@example.invalid
& git config user.name audit
& git add go.mod p.go p_test.go leak.pem leak.env
$stagedOut = (& $kern verify --staged security 2>&1) -join "`n"
$stagedExit = $LASTEXITCODE
Write-Host (First-Lines $stagedOut 5)
Record "11-verify-staged-critical" ("exit=$stagedExit contains-FAIL=" + ($stagedOut -match "FAIL") + " (exit 0 + FAIL = gate bypass)")
Pop-Location

# ---------- 7. setup write behavior ----------
$setupDir = Join-Path $root "setupdir"
New-Item -ItemType Directory -Force -Path $setupDir | Out-Null
Push-Location $setupDir
$null = & $kern setup --dry-run --plain 2>&1
$dryCount = (Get-ChildItem -Recurse -File -ErrorAction SilentlyContinue).Count
$null = & $kern setup --plain 2>&1
$realCount = (Get-ChildItem -Recurse -File -ErrorAction SilentlyContinue).Count
Record "12-setup-dry-run-files" $dryCount
Record "13-setup-real-files" $realCount
Pop-Location

# ---------- 8. MCP handshake + tool count ----------
$init = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"audit","version":"1"}}}'
$list = '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
$mcpOut = @($init, $list) | & $mcp 2>$null
$tools = 0
foreach ($line in $mcpOut) {
    try { $o = $line | ConvertFrom-Json; if ($o.id -eq 2) { $tools = $o.result.tools.Count } } catch {}
}
Record "14-mcp-tools" $tools

# ---------- 9. exec isolation posture ----------
$py = Get-Command python -ErrorAction SilentlyContinue
$node = Get-Command node -ErrorAction SilentlyContinue
$lang = $null; $code = $null
if ($py) { $lang = "python"; $code = "print('hi')" }
elseif ($node) { $lang = "node"; $code = "console.log('hi')" }
if ($lang) {
    Remove-Item Env:KERN_ALLOW_UNISOLATED -ErrorAction SilentlyContinue
    $ex1 = (& $kern exec --lang $lang $code 2>&1) -join "`n"
    Record "15-exec-default" ("exit=$LASTEXITCODE | " + (First-Lines $ex1 1))
    $env:KERN_ALLOW_UNISOLATED = "1"
    $ex2 = (& $kern exec --lang $lang $code 2>&1) -join "`n"
    Record "16-exec-override" ("exit=$LASTEXITCODE | " + (First-Lines $ex2 1))
    Remove-Item Env:KERN_ALLOW_UNISOLATED -ErrorAction SilentlyContinue
} else {
    Record "15-exec-default" "SKIP (no python/node)"
}

# ---------- 10. global hook end-to-end (isolated global git config) ----------
& $kern setup --global-only --global-rules --plain 2>&1 | Out-Null
$hooks = Join-Path $env:USERPROFILE ".kern\git-hooks\pre-commit"
Record "17-global-hook-installed" (Test-Path $hooks)
if (Test-Path $hooks) {
    $env:PATH = "$root;$env:PATH"
    $env:KERN_BINARY = "kern.exe"
    Push-Location $proj
    $commitOut = (& git -c core.hooksPath=(Split-Path $hooks) commit -m "audit: committed leak.env" 2>&1) -join "`n"
    $commitExit = $LASTEXITCODE
    Write-Host (First-Lines $commitOut 6)
    Record "18-hook-commit" ("exit=$commitExit (0 = commit NOT blocked; check verdict above)")
    Pop-Location
    Remove-Item Env:KERN_BINARY -ErrorAction SilentlyContinue
}

# ---------- 11. machine secrets location (what uninstall leaves) ----------
$keyDir = Join-Path $env:APPDATA "kern"
Record "19-appdata-kern-exists" (Test-Path $keyDir)
if (Test-Path $keyDir) {
    Record "20-appdata-kern-files" ((Get-ChildItem $keyDir | ForEach-Object { $_.Name }) -join ", ")
}

# ---------- 12. doctor snapshot ----------
$doc = (& $kern doctor --json 2>&1) -join "`n"
$docPath = Join-Path $root "doctor.json"
WriteText $docPath $doc
$fail = ([regex]::Matches($doc, '"level":\s*"fail"')).Count
Record "21-doctor-fail-count" $fail

# ---------- summary ----------
Write-Host ""
Write-Host "================ SUMMARY ================"
foreach ($r in $results) { Write-Host ("{0,-28} {1}" -f $r.Check, $r.Result) }
$summaryPath = Join-Path $root "summary.txt"
$results | Format-Table -AutoSize | Out-String | Set-Content -Path $summaryPath -Encoding UTF8
Write-Host ""
Write-Host "Transcript artifacts: $root"
Write-Host "  summary.txt  doctor.json"
Write-Host "Paste summary.txt + doctor.json (or this console output) back for triage."
Write-Host "Delete the folder when done:  Remove-Item -Recurse -Force '$root'"
