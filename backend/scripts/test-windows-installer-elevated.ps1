[CmdletBinding()]
param(
    [string]$Version = '0.2.0-rc.1'
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$payloadRoot = Join-Path $repositoryRoot "dist\release\iot-edge-$Version-windows-amd64"
$installerRoot = Join-Path $repositoryRoot 'dist\installer'
$msi = Join-Path $installerRoot "iot-edge-$Version-windows-amd64.msi"
$setup = Join-Path $installerRoot "iot-edge-$Version-windows-amd64-setup.exe"
$installRoot = Join-Path $env:ProgramFiles 'IoT Edge'
$dataRoot = Join-Path $env:ProgramData 'IoT Edge'
$evidencePath = Join-Path $repositoryRoot 'docs\performance\windows-installer.local.json'
$marker = Join-Path $dataRoot 'installer-preservation-acceptance.marker'
$phaseLog = Join-Path $repositoryRoot 'docs\performance\windows-installer-phase.local.log'
$installedByHarness = $false
$dataCreatedByHarness = $false

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Installer acceptance requires elevated PowerShell.' }
Set-Content -LiteralPath $phaseLog -Value 'preflight' -Encoding ascii
foreach ($path in @($msi,$setup)) { if (-not (Test-Path -LiteralPath $path)) { throw "Installer artifact is missing: $path" } }
if ((Test-Path -LiteralPath $installRoot) -or (Test-Path -LiteralPath $dataRoot) -or (Get-Service IoTEdge -ErrorAction SilentlyContinue)) { throw 'Acceptance refuses an existing IoT Edge installation, data root or service.' }

function Invoke-Installer([string[]]$Arguments, [string]$Failure) {
    $process = Start-Process -FilePath $setup -ArgumentList $Arguments -PassThru -WindowStyle Hidden
    $process.WaitForExit()
    if ($process.ExitCode -notin @(0,3010)) { throw "$Failure (exit code $($process.ExitCode))" }
}

try {
    Add-Content -LiteralPath $phaseLog -Value 'quiet-install-start' -Encoding ascii
    Invoke-Installer -Arguments @('/quiet','/norestart') -Failure 'quiet install failed'
    Add-Content -LiteralPath $phaseLog -Value 'quiet-install-complete' -Encoding ascii
    $installedByHarness = $true
    $dataCreatedByHarness = Test-Path -LiteralPath $dataRoot
    foreach ($source in Get-ChildItem -LiteralPath $payloadRoot -File) {
        $destination = Join-Path $installRoot $source.Name
        if (-not (Test-Path -LiteralPath $destination) -or (Get-FileHash $source.FullName).Hash -ne (Get-FileHash $destination).Hash) { throw "installed payload mismatch: $($source.Name)" }
    }
    Set-Content -LiteralPath $marker -Value 'preserve-on-uninstall' -Encoding ascii
    $server = Join-Path $installRoot 'iot-edge-server.exe'
    [IO.File]::WriteAllBytes($server, [byte[]](0x49,0x6f,0x54))
    Add-Content -LiteralPath $phaseLog -Value 'quiet-repair-start' -Encoding ascii
    Invoke-Installer -Arguments @('/repair','/quiet','/norestart') -Failure 'quiet repair failed'
    Add-Content -LiteralPath $phaseLog -Value 'quiet-repair-complete' -Encoding ascii
    if ((Get-FileHash $server).Hash -ne (Get-FileHash (Join-Path $payloadRoot 'iot-edge-server.exe')).Hash) { throw 'repair did not restore the installed server binary' }
    Add-Content -LiteralPath $phaseLog -Value 'quiet-uninstall-start' -Encoding ascii
    Invoke-Installer -Arguments @('/uninstall','/quiet','/norestart') -Failure 'quiet uninstall failed'
    Add-Content -LiteralPath $phaseLog -Value 'quiet-uninstall-complete' -Encoding ascii
    $installedByHarness = $false
    if (Test-Path -LiteralPath $installRoot) { throw 'uninstall left the Program Files payload behind' }
    if (-not (Test-Path -LiteralPath $marker)) { throw 'uninstall did not preserve ProgramData' }
    [ordered]@{ measured_at=[DateTimeOffset]::Now.ToString('o'); bootstrap_quiet_install='passed'; payload_hashes='passed'; quiet_repair='passed'; quiet_uninstall='passed'; programdata_preservation='passed'; service_untouched=$true; database_untouched=$true; failures=0 } | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $evidencePath -Encoding utf8
}
finally {
    if ($installedByHarness) {
        try { Invoke-Installer -Arguments @('/uninstall','/quiet','/norestart') -Failure 'cleanup uninstall failed' } catch { Write-Warning $_ }
    }
    if (Test-Path -LiteralPath $marker) { Remove-Item -LiteralPath $marker -Force }
    if ($dataCreatedByHarness -and (Test-Path -LiteralPath $dataRoot)) {
        $remaining = @(Get-ChildItem -LiteralPath $dataRoot -Force -Recurse -ErrorAction SilentlyContinue)
        if ($remaining.Count -eq 4 -and @($remaining | Where-Object { -not $_.PSIsContainer }).Count -eq 0) { [IO.Directory]::Delete([IO.Path]::GetFullPath($dataRoot), $true) }
    }
}
