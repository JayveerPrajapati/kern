# retarget.ps1 — repoint the project's module path and distribution (PowerShell).
#
# Usage:
#   .\scripts\retarget.ps1 -Module "github.com/yourname/kern"

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Module
)

$ErrorActionPreference = "Stop"

if ($Module -eq "github.com/JayveerPrajapati/kern") {
    Write-Error "Module must differ from the current module path"
    exit 1
}

$parts = $Module.Split('/')
if ($parts.Length -lt 2) {
    Write-Error "Invalid module path format. Expected format: github.com/owner/repo"
    exit 1
}
$owner = $parts[1]

Write-Host "==> retargeting module path to $Module (owner $owner)"

# 1. Update go.mod
$goMod = Get-Content "go.mod" -Raw
$goMod = $goMod -replace "(?m)^module github\.com/JayveerPrajapati/kern$", "module $Module"
Set-Content -Path "go.mod" -Value $goMod -NoNewline

# 2. Update Go sources
$goFiles = Get-ChildItem -Path @("cmd", "internal", ".github") -Recurse -Filter "*.go" -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName }
foreach ($file in $goFiles) {
    $c = Get-Content $file -Raw
    if ($c.Contains("github.com/JayveerPrajapati/kern/")) {
        $c = $c.Replace("github.com/JayveerPrajapati/kern/", "$Module/")
        Set-Content -Path $file -Value $c -NoNewline
    }
}

# 3. Distribution placeholders
$distFiles = @(
    "install.sh",
    "install.ps1",
    "homebrew\kern.rb",
    "python\pyproject.toml",
    "python\kern\_bootstrap.py",
    "python\README.md",
    "README.md",
    ".github\workflows\release.yml"
)

foreach ($rel in $distFiles) {
    if (Test-Path $rel) {
        $c = Get-Content $rel -Raw
        $c = $c.Replace("JayveerPrajapati", $owner)
        $c = $c.Replace("github.com/JayveerPrajapati/kern", $Module)
        Set-Content -Path $rel -Value $c -NoNewline
    }
}

Write-Host "==> done. Verify with: go build ./... && go test ./..."
