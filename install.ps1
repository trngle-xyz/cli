# Install trngle CLI for Windows — https://trngle.xyz
# Usage: irm https://cli.trngle.xyz/install.ps1 | iex
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "trngle-xyz/cli"
$Binary = "trngle"
$InstallDir = "$env:LOCALAPPDATA\trngle"

Write-Host ""
Write-Host "  ▲ trngle CLI installer" -ForegroundColor Cyan
Write-Host "  ─────────────────────"
Write-Host ""

# Get latest release
Write-Host "  ◐ Finding latest release..." -NoNewline
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Tag = $Release.tag_name
Write-Host " $Tag"

# Download
$Filename = "$Binary-windows-amd64.exe"
$Url = "https://github.com/$Repo/releases/download/$Tag/$Filename"
Write-Host "  ◐ Downloading $Filename..." -NoNewline

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$OutPath = Join-Path $InstallDir "$Binary.exe"
Invoke-WebRequest -Uri $Url -OutFile $OutPath
Write-Host " done"

Write-Host "  ✓ Installed to $OutPath" -ForegroundColor Green

# Add to PATH if not already there
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "  ✓ Added $InstallDir to PATH" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Open a new terminal for PATH changes to take effect."
}

Write-Host ""
Write-Host "  ▲ trngle $Tag ready" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Get started:"
Write-Host "    trngle"
Write-Host ""
