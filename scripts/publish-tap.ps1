# publish-tap.ps1 — create or update the Homebrew tap (PowerShell).
#
# Usage:
#   .\scripts\publish-tap.ps1 -Tag "v1.2.3" [-Owner "username"]

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Tag,

    [Parameter(Position = 1)]
    [string]$Owner = ""
)

$ErrorActionPreference = "Stop"

if (-not $Owner) {
    $resolvedOwner = (gh repo view --json owner --jq .owner.login 2>$null)
    if ($LASTEXITCODE -eq 0 -and $resolvedOwner) {
        $Owner = $resolvedOwner
    } else {
        $Owner = "JayveerPrajapati"
    }
}

$kernRepo = if ($env:KERN_REPO) { $env:KERN_REPO } else { "$Owner/kern" }
$tapRepo = "$Owner/homebrew-tap"

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmpDir | Out-Null

try {
    $archiveUrl = "https://github.com/$kernRepo/archive/refs/tags/$Tag.tar.gz"
    Write-Host "==> fetching source archive: $archiveUrl"
    $sourceTarball = Join-Path $tmpDir "source.tar.gz"
    Invoke-WebRequest -Uri $archiveUrl -OutFile $sourceTarball
    $sha256 = (Get-FileHash -Path $sourceTarball -Algorithm SHA256).Hash.ToLower()
    Write-Host "==> source sha256: $sha256"

    Write-Host "==> generating formula for $Tag"
    $formulaTemplate = Join-Path $PSScriptRoot "..\homebrew\kern.rb"
    $content = Get-Content $formulaTemplate -Raw
    $content = $content -replace "JayveerPrajapati/kern", $kernRepo
    $content = $content -replace "__KERN_VERSION__", $Tag
    $content = $content -replace "__KERN_SHA256__", $sha256

    $kernRb = Join-Path $tmpDir "kern.rb"
    Set-Content -Path $kernRb -Value $content -NoNewline

    $tapDir = Join-Path $tmpDir "tap"
    gh repo view $tapRepo 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "==> tap repo exists, cloning $tapRepo"
        gh repo clone $tapRepo $tapDir
    } else {
        Write-Host "==> creating public tap repo $tapRepo"
        gh repo create $tapRepo --public --clone --description "Homebrew tap for kern"
        Move-Item (Join-Path $tmpDir "homebrew-tap") $tapDir
    }

    $formulaDir = Join-Path $tapDir "Formula"
    if (-not (Test-Path $formulaDir)) {
        New-Item -ItemType Directory -Path $formulaDir | Out-Null
    }
    Copy-Item $kernRb (Join-Path $formulaDir "kern.rb") -Force

    Push-Location $tapDir
    try {
        git add Formula/kern.rb
        git diff --cached --quiet
        if ($LASTEXITCODE -eq 0) {
            Write-Host "==> formula unchanged, nothing to push"
            return
        }
        git -c "user.name=$Owner" -c "user.email=$Owner@users.noreply.github.com" commit -m "kern $Tag: update formula"
        git push
        Write-Host "==> done: brew tap $Owner/tap && brew install kern"
    } finally {
        Pop-Location
    }
} finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
