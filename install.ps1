# Install trngle CLI for Windows — https://trngle.xyz
# Usage: irm https://cli.trngle.xyz/install.ps1 | iex
$ErrorActionPreference = "Stop"

$Repo = "trngle-xyz/cli"
$Binary = "trngle"
$InstallDir = "$env:LOCALAPPDATA\trngle"

# Get latest release
Write-Host "Finding latest release..."
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Tag = $Release.tag_name
Write-Host "Latest version: $Tag"

# Download
$Filename = "$Binary-windows-amd64.exe"
$Url = "https://github.com/$Repo/releases/download/$Tag/$Filename"
Write-Host "Downloading $Filename..."

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$OutPath = Join-Path $InstallDir "$Binary.exe"
Invoke-WebRequest -Uri $Url -OutFile $OutPath

# Add to PATH if not already there
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "Added $InstallDir to PATH"
}

Write-Host ""
Write-Host "trngle $Tag installed to $OutPath" -ForegroundColor Green
Write-Host ""
Write-Host "Get started (open a new terminal if needed):"
Write-Host "  trngle"
Write-Host ""
