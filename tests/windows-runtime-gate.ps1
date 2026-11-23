$ErrorActionPreference = 'Stop'

try {
  $ErrorActionPreference = 'Stop'
  $root = Join-Path $env:RUNNER_TEMP 'YourLM-Windows-Gate'
  $env:LOCALAPPDATA = Join-Path $root 'LocalAppData'
  $env:APPDATA = Join-Path $root 'AppDataRoaming'
  $env:YOURLM_DISABLE_DRIVE_MIGRATION = '1'
  $source = (Resolve-Path 'packages\YourLM.exe').Path
  $installed = Join-Path $env:LOCALAPPDATA 'Programs\YourLM\YourLM.exe'
  $shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\YourLM.lnk'
  $uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\YourLM'

  $install = Start-Process -FilePath $source -ArgumentList '--install-only' -Wait -PassThru
  if ($install.ExitCode -ne 0) { throw "Per-user installation exited with $($install.ExitCode)" }
  if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) { throw 'Per-user EXE was not installed' }
  if (-not (Test-Path -LiteralPath $shortcut -PathType Leaf)) { throw 'Start Menu shortcut was not created' }
  if ((Get-ItemProperty -LiteralPath $uninstallKey).DisplayVersion -ne '0.2.0-rc17') { throw 'Installed Apps version is incorrect' }

  $controllerOut = Join-Path $root 'controller.out'
  $controllerErr = Join-Path $root 'controller.err'
  $process = Start-Process -FilePath $installed -ArgumentList '--no-browser' -RedirectStandardOutput $controllerOut -RedirectStandardError $controllerErr -PassThru
  $controllerText = ''
  for ($attempt = 0; $attempt -lt 60; $attempt++) {
    Start-Sleep -Milliseconds 500
    $process.Refresh()
    if ($process.HasExited) { throw "YourLM exited during first run with $($process.ExitCode): $(Get-Content -Raw -ErrorAction SilentlyContinue $controllerErr)" }
    if (Test-Path -LiteralPath $controllerOut) { $controllerText = Get-Content -Raw -LiteralPath $controllerOut }
    if ($controllerText -match '(?m)^http://127\.0\.0\.1:[0-9]+/#[0-9a-f]+$') { break }
  }
  $controllerMatch = [regex]::Match($controllerText, '(?m)^(http://127\.0\.0\.1:[0-9]+)/#([0-9a-f]+)$')
  if (-not $controllerMatch.Success) { throw "Controller URL was not emitted: $controllerText" }
  $controllerBase = $controllerMatch.Groups[1].Value
  $controllerHeaders = @{ 'X-YourLM' = $controllerMatch.Groups[2].Value }

  $component = Get-ChildItem (Join-Path $env:APPDATA 'YourLM\components') -Directory | Select-Object -First 1
  if (-not $component) { throw 'First run did not extract private components' }
  $required = @(
    'runtime\llama-server.exe',
    'runtime\ggml-rpc-server.exe',
    'platform-tools\adb.exe',
    'platform-tools\AdbWinApi.dll',
    'platform-tools\AdbWinUsbApi.dll',
    'YourLM-Android-arm64.apk',
    '.complete'
  )
  foreach ($name in $required) {
    if (-not (Test-Path (Join-Path $component.FullName $name))) { throw "Missing extracted component: $name" }
  }
  $embeddedApk = Get-FileHash -Algorithm SHA256 (Join-Path $component.FullName 'YourLM-Android-arm64.apk')
  $releaseApk = Get-FileHash -Algorithm SHA256 'packages\YourLM-Android-arm64.apk'
  if ($embeddedApk.Hash -ne $releaseApk.Hash) { throw 'Embedded APK differs from release APK' }

  $initialState = Invoke-RestMethod -Uri "$controllerBase/api/state" -Headers $controllerHeaders
  if ($initialState.diagnostics.recommendation.readyToApply) { throw 'Autopilot was enabled before collecting hardware facts' }
  Invoke-RestMethod -Method Post -Uri "$controllerBase/api/diagnostics-start" -Headers $controllerHeaders -ContentType 'application/json' -Body '{}' | Out-Null
  $diagnosticState = $null
  for ($attempt = 0; $attempt -lt 180; $attempt++) {
    Start-Sleep -Milliseconds 500
    $diagnosticState = Invoke-RestMethod -Uri "$controllerBase/api/state" -Headers $controllerHeaders
    if (-not $diagnosticState.diagnostics.running) { break }
  }
  if (-not $diagnosticState.diagnostics.complete) { throw "Windows diagnostics did not complete: $($diagnosticState.diagnostics.error)" }
  if (-not $diagnosticState.diagnostics.host.cpu -or $diagnosticState.diagnostics.host.cpu -eq '0') { throw 'Windows diagnostics did not identify the CPU' }
  if ($diagnosticState.diagnostics.benchmark.cpuHashBps -le 0 -or $diagnosticState.diagnostics.benchmark.diskWriteBps -le 0) { throw 'Windows diagnostics did not measure CPU and managed storage' }
  if ($diagnosticState.diagnostics.recommendation.selectedId -ne 'qwen36-35b-a3b-abliterated-balanced') { throw 'Autopilot selected an unaudited recipe' }
  if ($diagnosticState.diagnostics.recommendation.readyToApply) { throw 'Autopilot unlocked while safe D: storage was unavailable' }
  if ($diagnosticState.diagnostics.recommendation.nextStep -notmatch 'migracj') { throw 'Autopilot did not explain its D: storage prerequisite' }
  if (-not (Test-Path -LiteralPath $diagnosticState.diagnostics.reportPath -PathType Leaf)) { throw 'Diagnostics report was not persisted' }

  Stop-Process -Id $process.Id -Force
  & (Join-Path $component.FullName 'platform-tools\adb.exe') kill-server 2>$null

  $native = Start-Process -FilePath $installed -PassThru
  $windowReady = $false
  for ($attempt = 0; $attempt -lt 30; $attempt++) {
    Start-Sleep -Seconds 1
    $native.Refresh()
    if ($native.HasExited) { throw "YourLM native window exited early with $($native.ExitCode)" }
    if ($native.MainWindowHandle -ne 0 -and $native.MainWindowTitle -like 'YourLM*') {
      $windowReady = $true
      break
    }
  }
  if (-not $windowReady) { throw 'YourLM did not create its native WebView2 application window' }
  Stop-Process -Id $native.Id -Force

  $remove = Start-Process -FilePath $installed -ArgumentList '--uninstall' -Wait -PassThru
  if ($remove.ExitCode -ne 0) { throw "Uninstall exited with $($remove.ExitCode)" }
  for ($attempt = 0; $attempt -lt 80 -and (Test-Path -LiteralPath $installed); $attempt++) { Start-Sleep -Milliseconds 250 }
  if (Test-Path -LiteralPath $installed) { throw 'Uninstall did not remove the installed EXE' }
  if (Test-Path -LiteralPath $shortcut) { throw 'Uninstall did not remove the Start Menu shortcut' }
  if (Test-Path -LiteralPath $uninstallKey) { throw 'Uninstall did not remove the Installed Apps entry' }

  # Exercise the RC17 managed-drive path and verified migration using a
  # fixed-volume directory override. This keeps the hosted runner
  # deterministic even though its real D: scratch disk varies.
  Remove-Item Env:YOURLM_DISABLE_DRIVE_MIGRATION
  $managedRoot = Join-Path $root 'Managed-D-YourLM'
  $env:YOURLM_WINDOWS_ROOT = $managedRoot
  $env:APPDATA = Join-Path $root 'MigrationAppDataRoaming'
  $shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\YourLM.lnk'
  $legacyData = Join-Path $env:APPDATA 'YourLM'
  New-Item -ItemType Directory -Force (Join-Path $legacyData 'models') | Out-Null
  $migrationFixture = 'verified migration fixture with a resumable prefix'
  [IO.File]::WriteAllText((Join-Path $legacyData 'models\resume.gguf.part'), $migrationFixture)
  $managedExe = Join-Path $managedRoot 'App\YourLM.exe'
  $managedData = Join-Path $managedRoot 'Data'
  # Reproduce the field failure exactly: RC16 left a valid prefix at
  # Data.migrating\...migrating-part. RC17 must validate and append it,
  # not delete the source and not fail with ERROR_FILE_EXISTS.
  $stagingModel = Join-Path $managedRoot 'Data.migrating\models\resume.gguf.part.migrating-part'
  New-Item -ItemType Directory -Force (Split-Path -Parent $stagingModel) | Out-Null
  [IO.File]::WriteAllText($stagingModel, $migrationFixture.Substring(0, 19))

  $managedInstall = Start-Process -FilePath $source -ArgumentList '--install-only' -Wait -PassThru
  if ($managedInstall.ExitCode -ne 0) { throw "Managed-drive installation exited with $($managedInstall.ExitCode)" }
  if (-not (Test-Path -LiteralPath $managedExe -PathType Leaf)) { throw 'EXE was not installed in the managed drive root' }
  if ((Get-ItemProperty -LiteralPath $uninstallKey).InstallLocation -ne (Split-Path -Parent $managedExe)) { throw 'Installed Apps did not move to the managed drive root' }
  $link = (New-Object -ComObject WScript.Shell).CreateShortcut($shortcut)
  if ($link.TargetPath -ne $managedExe) { throw 'Start Menu shortcut did not move to the managed drive root' }

  $managedOut = Join-Path $root 'managed-controller.out'
  $managedErr = Join-Path $root 'managed-controller.err'
  $managedProcess = Start-Process -FilePath $managedExe -ArgumentList '--no-browser' -RedirectStandardOutput $managedOut -RedirectStandardError $managedErr -PassThru
  $managedText = ''
  for ($attempt = 0; $attempt -lt 60; $attempt++) {
    Start-Sleep -Milliseconds 500
    $managedProcess.Refresh()
    if ($managedProcess.HasExited) { throw "Managed-drive first run exited with $($managedProcess.ExitCode): $(Get-Content -Raw -ErrorAction SilentlyContinue $managedErr)" }
    if (Test-Path -LiteralPath $managedOut) { $managedText = Get-Content -Raw -LiteralPath $managedOut }
    if ((Test-Path -LiteralPath (Join-Path $managedData '.yourlm-storage-v1')) -and $managedText -match '(?m)^http://127\.0\.0\.1:[0-9]+/#[0-9a-f]+$') { break }
  }
  $managedMatch = [regex]::Match($managedText, '(?m)^(http://127\.0\.0\.1:[0-9]+)/#([0-9a-f]+)$')
  if (-not $managedMatch.Success) { throw "Managed controller URL was not emitted: $managedText" }
  $managedBase = $managedMatch.Groups[1].Value
  $managedHeaders = @{ 'X-YourLM' = $managedMatch.Groups[2].Value }
  $managedState = Invoke-RestMethod -Uri "$managedBase/api/state" -Headers $managedHeaders
  if ($managedState.storage.migration.status -ne 'recovered') { throw "Stale part was not reported as recovered: $($managedState.storage.migration.status)" }
  if ($managedState.storage.migration.resumedFiles -ne 1 -or $managedState.storage.migration.resumedBytes -ne 19) { throw 'Resumed migration metrics are incorrect' }
  if ($managedState.storage.dataPath -ne $managedData) { throw 'Published D: storage is not active in application state' }
  $storageKey = 'HKCU:\Software\YourLM'
  if ((Get-ItemProperty -LiteralPath $storageKey).StorageRoot -ne $managedRoot) { throw 'Managed storage root was not persisted for future updates' }
  if (-not (Test-Path -LiteralPath (Join-Path $managedData 'models\resume.gguf.part') -PathType Leaf)) { throw 'Existing model fragment was not migrated to managed storage' }
  if ((Get-Content -Raw -LiteralPath (Join-Path $managedData 'models\resume.gguf.part')) -ne $migrationFixture) { throw 'Resumed model fragment changed' }
  if (Test-Path -LiteralPath $stagingModel) { throw 'The .migrating-part file leaked into published storage' }
  if (Test-Path -LiteralPath (Join-Path $managedRoot 'Data.migrating')) { throw 'Staging directory remained after atomic publication' }
  if (Test-Path -LiteralPath $legacyData) { throw 'Verified legacy data was not retired after migration' }
  $migrationLock = Join-Path $managedRoot '.yourlm-data-migration.lock'
  if (-not (Test-Path -LiteralPath $migrationLock -PathType Leaf)) { throw 'Migration lock evidence is missing' }
  if ((Get-Item -LiteralPath $migrationLock).Length -ne 0) { throw 'Migration lock remained owned after startup' }

  if ($managedState.diagnostics.recommendation.readyToApply) { throw 'Managed autopilot unlocked before collecting hardware facts' }
  Invoke-RestMethod -Method Post -Uri "$managedBase/api/diagnostics-start" -Headers $managedHeaders -ContentType 'application/json' -Body '{}' | Out-Null
  $managedDiagnosticState = $null
  for ($attempt = 0; $attempt -lt 180; $attempt++) {
    Start-Sleep -Milliseconds 500
    $managedDiagnosticState = Invoke-RestMethod -Uri "$managedBase/api/state" -Headers $managedHeaders
    if (-not $managedDiagnosticState.diagnostics.running) { break }
  }
  if (-not $managedDiagnosticState.diagnostics.complete) { throw "Managed-storage diagnostics did not complete: $($managedDiagnosticState.diagnostics.error)" }
  if ($managedDiagnosticState.diagnostics.recommendation.selectedId -ne 'qwen36-35b-a3b-abliterated-balanced') { throw 'Managed autopilot selected an unaudited recipe' }
  if (-not $managedDiagnosticState.diagnostics.recommendation.readyToApply) { throw "Autopilot stayed blocked after verified D: publication: $($managedDiagnosticState.diagnostics.recommendation.nextStep)" }

  # The session owner keeps its source/destination guard after migration
  # returns. A second instance must fail quickly instead of opening a
  # second writer or starting another snapshot under the active app.
  $secondOut = Join-Path $root 'second-instance.out'
  $secondErr = Join-Path $root 'second-instance.err'
  $second = Start-Process -FilePath $managedExe -ArgumentList '--no-browser' -RedirectStandardOutput $secondOut -RedirectStandardError $secondErr -PassThru
  if (-not $second.WaitForExit(10000)) {
    Stop-Process -Id $second.Id -Force
    throw 'Second managed instance did not stop at the session lock'
  }
  if ($second.ExitCode -eq 0) { throw 'Second managed instance bypassed the session lock' }
  $secondError = Get-Content -Raw -ErrorAction SilentlyContinue $secondErr
  if ($secondError -notmatch 'inne okno YourLM') { throw "Second instance did not report the active owner: $secondError" }
  $managedProcess.Refresh()
  if ($managedProcess.HasExited) { throw 'Second instance disturbed the active managed owner' }
  Stop-Process -Id $managedProcess.Id -Force

  $managedRemove = Start-Process -FilePath $managedExe -ArgumentList '--uninstall' -Wait -PassThru
  if ($managedRemove.ExitCode -ne 0) { throw "Managed uninstall exited with $($managedRemove.ExitCode)" }
  for ($attempt = 0; $attempt -lt 80 -and (Test-Path -LiteralPath $managedExe); $attempt++) { Start-Sleep -Milliseconds 250 }
  if (Test-Path -LiteralPath $managedExe) { throw 'Managed uninstall did not remove the EXE' }
  if (-not (Test-Path -LiteralPath $managedData)) { throw 'Managed uninstall removed the private model library' }
} catch {
  $detail = ($_ | Format-List * -Force | Out-String)
  if (-not $detail) { $detail = "$_" }
  $escaped = $detail.Replace('%', '%25').Replace("`r", '%0D').Replace("`n", '%0A')
  Write-Host "::error title=Windows runtime integration failed::$escaped"
  Write-Error $detail
  exit 1
}
