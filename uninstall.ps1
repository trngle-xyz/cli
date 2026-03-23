# Uninstall trngle CLI for Windows
# Usage: irm https://raw.githubusercontent.com/trngle-xyz/cli/main/uninstall.ps1 | iex
$ErrorActionPreference = "Stop"

$Binary = "trngle"
$InstallDir = "$env:LOCALAPPDATA\trngle"
$ExePath = Join-Path $InstallDir "$Binary.exe"
$ConfigDir = Join-Path $env:USERPROFILE ".trngle"

# Remove binary
if (Test-Path $ExePath) {
    Remove-Item $ExePath -Force
    Write-Host "Removed $ExePath"
} else {
    Write-Host "trngle binary not found at $ExePath"
}

# Remove install directory if empty
if ((Test-Path $InstallDir) -and @(Get-ChildItem $InstallDir).Count -eq 0) {
    Remove-Item $InstallDir -Force
}

# Remove from PATH
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -like "*$InstallDir*") {
    $NewPath = ($UserPath -split ";" | Where-Object { $_ -ne $InstallDir }) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
    Write-Host "Removed $InstallDir from PATH"
}

# Optionally remove config
if (Test-Path $ConfigDir) {
    $answer = Read-Host "Remove config and history at $ConfigDir? [y/N]"
    if ($answer -match "^[yY]") {
        Remove-Item $ConfigDir -Recurse -Force
        Write-Host "Removed $ConfigDir"
    } else {
        Write-Host "Kept $ConfigDir"
    }
}

Write-Host ""
Write-Host "trngle uninstalled."
