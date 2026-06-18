#Requires -Version 5.1
<#
.SYNOPSIS
    Installs or removes wsl-ssh-pageant from the Windows Startup folder.

.PARAMETER PipeName
    Name of the named pipe exposed to Windows OpenSSH. Default: ssh-pageant
    The pipe will be available as \\.\pipe\<PipeName>.

.PARAMETER WslSocket
    Path to a Unix socket for WSL passthrough. Optional.
    Example: C:\Users\you\ssh-agent.sock

.PARAMETER NoSystray
    Disable the system tray icon.

.PARAMETER Uninstall
    Remove the startup shortcut instead of creating it.

.EXAMPLE
    .\install.ps1
    Creates shortcut with defaults: --systray --winssh ssh-pageant

.EXAMPLE
    .\install.ps1 -PipeName my-agent -NoSystray

.EXAMPLE
    .\install.ps1 -Uninstall
#>
param (
    [string] $PipeName  = "ssh-pageant",
    [string] $WslSocket = "",
    [switch] $NoSystray,
    [switch] $Uninstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$BinaryName   = "wsl-ssh-pageant-amd64-gui.exe"
$ScriptDir    = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinaryPath   = Join-Path $ScriptDir $BinaryName
$StartupDir   = [System.Environment]::GetFolderPath("Startup")
$ShortcutPath = Join-Path $StartupDir "wsl-ssh-pageant.lnk"

if ($Uninstall) {
    if (Test-Path $ShortcutPath) {
        Remove-Item $ShortcutPath -Force
        Write-Host "Shortcut removed: $ShortcutPath"
    } else {
        Write-Host "Shortcut not found, nothing to remove."
    }
    exit 0
}

if (-not (Test-Path $BinaryPath)) {
    Write-Error "Binary not found: $BinaryPath`nPlace install.ps1 in the same directory as $BinaryName."
    exit 1
}

$args = @()
if (-not $NoSystray)  { $args += "--systray" }
if ($PipeName)        { $args += "--winssh", $PipeName }
if ($WslSocket)       { $args += "--wsl", $WslSocket }

$Arguments = $args -join " "

$Shell            = New-Object -ComObject WScript.Shell
$Shortcut         = $Shell.CreateShortcut($ShortcutPath)
$Shortcut.TargetPath       = $BinaryPath
$Shortcut.Arguments        = $Arguments
$Shortcut.WorkingDirectory = $ScriptDir
$Shortcut.Description      = "WSL SSH Pageant agent"
$Shortcut.Save()

Write-Host "Shortcut created: $ShortcutPath"
Write-Host "Command: $BinaryPath $Arguments"
Write-Host ""
Write-Host "The agent will start automatically at next login."
Write-Host "To start it now, run: Start-Process '$BinaryPath' -ArgumentList '$Arguments'"
