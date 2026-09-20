# brew-release.ps1 — generate a Homebrew formula with real SHA256 sums (PowerShell).
#
# Usage:
#   .\scripts\brew-release.ps1 -Version "v1.2.3"
#   .\scripts\brew-release.ps1 -Version "v1.2.3" -Tarball "path\to\tarball"

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Version,

    [Parameter(Position = 1)]
    [string]$Tarball = ""
)

$ErrorActionPreference = "Stop"

$repo = if ($env:KERN_REPO) { $env:KERN_REPO } else { "JayveerPrajapati/kern" }

if ($Tarball -ne "") {
    if (-not (Test-Path $Tarball)) {
        Write-Error "tarball not found: $Tarball"
        exit 1
    }
    $sha256 = (Get-FileHash -Path $Tarball -Algorithm SHA256).Hash.ToLower()
} else {
    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Path $tmpDir | Out-Null
    try {
        $sourceUrl = "https://github.com/$repo/archive/refs/tags/$Version.tar.gz"
        $tmpTarball = Join-Path $tmpDir "kern.tar.gz"
        Write-Host "fetching source tarball: $sourceUrl" -ForegroundColor Cyan
        Invoke-WebRequest -Uri $sourceUrl -OutFile $tmpTarball
        $sha256 = (Get-FileHash -Path $tmpTarball -Algorithm SHA256).Hash.ToLower()
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }
}

$formulaTemplate = Join-Path $PSScriptRoot "..\homebrew\kern.rb"
$content = Get-Content $formulaTemplate -Raw
$content = $content -replace "JayveerPrajapati/kern", $repo
$content = $content -replace "__KERN_VERSION__", $Version
$content = $content -replace "__KERN_SHA256__", $sha256

[Console]::Out.Write($content)
[Console]::Error.WriteLine("# sha256: $sha256")
[Console]::Error.WriteLine("# note: set `$env:KERN_REPO=`"<you>/kern`" to fill the repo placeholder.")
