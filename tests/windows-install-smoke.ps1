$ErrorActionPreference = 'Stop'

$package = $args[0]
if (-not $package) { throw 'Pass the downloaded RC18 artifact directory' }
$source = Get-ChildItem -Path $package -Recurse -Filter 'YourLM.exe' | Select-Object -First 1
if (-not $source) { throw 'Validated artifact does not contain YourLM.exe' }

$testRoot = Join-Path $env:RUNNER_TEMP 'YourLM-Install-Smoke'
Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
$env:LOCALAPPDATA = Join-Path $testRoot 'LocalAppData'
$env:APPDATA = Join-Path $testRoot 'AppDataRoaming'
$env:YOURLM_DISABLE_DRIVE_MIGRATION = '1'
$installed = Join-Path $env:LOCALAPPDATA 'Programs\YourLM\YourLM.exe'
$shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\YourLM.lnk'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\YourLM'
Remove-Item -LiteralPath $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue

$install = Start-Process -FilePath $source.FullName -ArgumentList '--install-only' -Wait -PassThru
if ($install.ExitCode -ne 0) { throw "Installer exited with $($install.ExitCode)" }
if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) { throw 'Per-user EXE was not installed' }
if (-not (Test-Path -LiteralPath $shortcut -PathType Leaf)) { throw 'Start Menu shortcut was not created' }
if (-not (Test-Path -LiteralPath $uninstallKey)) { throw 'Installed Apps registry entry was not created' }

$entry = Get-ItemProperty -LiteralPath $uninstallKey
if ($entry.DisplayName -ne 'YourLM') { throw 'Unexpected Installed Apps display name' }
if ($entry.DisplayVersion -ne '0.2.0-rc18') { throw "Unexpected installed version: $($entry.DisplayVersion)" }
if ($entry.InstallLocation -ne (Split-Path -Parent $installed)) { throw 'Installed Apps path does not match the per-user EXE' }
if ($entry.UninstallString -notlike '*YourLM.exe*--uninstall*') { throw 'Uninstall command is missing' }

$shell = New-Object -ComObject WScript.Shell
$link = $shell.CreateShortcut($shortcut)
if ($link.TargetPath -ne $installed) { throw "Shortcut points to $($link.TargetPath), expected $installed" }

# Registration must be idempotent when the installed copy starts again.
$again = Start-Process -FilePath $installed -ArgumentList '--install-only' -Wait -PassThru
if ($again.ExitCode -ne 0) { throw "Idempotent registration exited with $($again.ExitCode)" }

# An older downloaded release must not replace a newer installed executable or
# downgrade the version shown by Installed Apps. A PE may safely carry overlay
# bytes, which gives this fixture a distinct hash while remaining executable.
$stream = [System.IO.File]::Open($installed, [System.IO.FileMode]::Append, [System.IO.FileAccess]::Write, [System.IO.FileShare]::Read)
try { $stream.WriteByte(0x52) } finally { $stream.Dispose() }
$newerHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $installed).Hash
Set-ItemProperty -LiteralPath $uninstallKey -Name DisplayVersion -Value '0.2.0-rc18'
$older = Start-Process -FilePath $source.FullName -ArgumentList '--install-only' -Wait -PassThru
if ($older.ExitCode -ne 0) { throw "Older installer check exited with $($older.ExitCode)" }
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $installed).Hash -ne $newerHash) { throw 'Older release replaced the newer installed EXE' }
if ((Get-ItemProperty -LiteralPath $uninstallKey).DisplayVersion -ne '0.2.0-rc18') { throw 'Older release downgraded Installed Apps metadata' }

$remove = Start-Process -FilePath $installed -ArgumentList '--uninstall' -Wait -PassThru
if ($remove.ExitCode -ne 0) { throw "Uninstaller exited with $($remove.ExitCode)" }
for ($i = 0; $i -lt 80 -and (Test-Path -LiteralPath $installed); $i++) {
  Start-Sleep -Milliseconds 250
}
if (Test-Path -LiteralPath $installed) { throw 'Uninstaller did not remove the per-user EXE' }
if (Test-Path -LiteralPath $shortcut) { throw 'Uninstaller did not remove the Start Menu shortcut' }
if (Test-Path -LiteralPath $uninstallKey) { throw 'Uninstaller did not remove the Installed Apps entry' }

Write-Host 'YourLM per-user install, Start Menu registration, downgrade protection, and uninstall passed.'
