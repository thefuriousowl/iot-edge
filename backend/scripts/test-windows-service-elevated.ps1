[CmdletBinding()]
param(
    [string]$EvidencePath = ''
)

$ErrorActionPreference = 'Stop'
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Windows Service acceptance requires an elevated PowerShell session.' }
if (Get-Service -Name IoTEdge -ErrorAction SilentlyContinue) { throw 'IoTEdge service already exists; refusing to modify an owner-managed service.' }

$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$evidenceFile = if ([string]::IsNullOrWhiteSpace($EvidencePath)) { Join-Path $repositoryRoot 'docs\performance\windows-service.local.json' } else { [IO.Path]::GetFullPath($EvidencePath) }
if (-not $evidenceFile.StartsWith($repositoryRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Service evidence path escaped the repository.' }
$programDataRoot = [IO.Path]::GetFullPath([Environment]::GetFolderPath('CommonApplicationData'))
$testRoot = [IO.Path]::GetFullPath((Join-Path $programDataRoot ('IoTEdgeServiceAcceptance-' + [guid]::NewGuid().ToString('N'))))
if (-not $testRoot.StartsWith($programDataRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Service acceptance path escaped ProgramData.' }
$fixturePath = Join-Path $testRoot 'iot-edge-server.exe'
$controlPath = Join-Path $testRoot 'iot-edge-service.exe'
$installed = $false
New-Item -ItemType Directory -Path $testRoot | Out-Null
try {
    Push-Location (Join-Path $repositoryRoot 'backend')
    try {
        $env:CGO_ENABLED='0'; $env:GOOS='windows'; $env:GOARCH='amd64'
        & go build -trimpath -ldflags '-s -w' -o $fixturePath ./cmd/service-smoke
        if ($LASTEXITCODE -ne 0) { throw 'Service fixture build failed' }
        & go build -trimpath -ldflags '-s -w' -o $controlPath ./cmd/service
        if ($LASTEXITCODE -ne 0) { throw 'Service control build failed' }
    }
    finally { Remove-Item Env:CGO_ENABLED,Env:GOOS,Env:GOARCH -ErrorAction SilentlyContinue; Pop-Location }

    & $controlPath install --exe $fixturePath
    if ($LASTEXITCODE -ne 0) { throw 'Service install failed' }; $installed = $true
    $service = Get-CimInstance Win32_Service -Filter "Name='IoTEdge'"
    if ($service.StartName -ne 'NT AUTHORITY\LocalService' -or $service.StartMode -ne 'Auto') { throw 'Service account/start-mode contract failed' }
    $recovery = (& sc.exe qfailure IoTEdge 2>&1) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $recovery -notmatch '5000' -or $recovery -notmatch '15000' -or $recovery -notmatch '60000') { throw 'Service recovery policy contract failed' }
    & $controlPath start; if ($LASTEXITCODE -ne 0) { throw 'Service start failed' }
    & $controlPath status; if ($LASTEXITCODE -ne 0 -or (Get-Service IoTEdge).Status -ne 'Running') { throw 'Service running status failed' }
    & $controlPath restart; if ($LASTEXITCODE -ne 0 -or (Get-Service IoTEdge).Status -ne 'Running') { throw 'Service restart failed' }
    & $controlPath stop; if ($LASTEXITCODE -ne 0 -or (Get-Service IoTEdge).Status -ne 'Stopped') { throw 'Service graceful stop failed' }
    & $controlPath uninstall; if ($LASTEXITCODE -ne 0) { throw 'Service uninstall failed' }; $installed = $false
    New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($evidenceFile)) -Force | Out-Null
    [ordered]@{
        measured_at = [DateTimeOffset]::Now.ToString('o'); platform = 'native Windows'; account = 'NT AUTHORITY\LocalService'
        automatic_start = 'passed'; recovery_delays_ms = @(5000,15000,60000); install = 'passed'; start = 'passed'
        status = 'passed'; restart = 'passed'; graceful_stop = 'passed'; uninstall = 'passed'; failures = 0
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $evidenceFile -Encoding utf8
    Write-Output 'Elevated Windows Service lifecycle acceptance passed.'
}
finally {
    if ($installed -and (Get-Service -Name IoTEdge -ErrorAction SilentlyContinue)) { & $controlPath uninstall 2>$null }
    if (Test-Path -LiteralPath $testRoot) { [IO.Directory]::Delete($testRoot, $true) }
}
