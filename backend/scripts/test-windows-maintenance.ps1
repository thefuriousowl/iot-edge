[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$backendRoot = Join-Path $repositoryRoot 'backend'
$evidencePath = Join-Path $repositoryRoot 'docs\performance\windows-maintenance.local.json'
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ("IoTEdgeMaintenanceAcceptance-" + [Guid]::NewGuid().ToString('N'))
$dataRoot = Join-Path $fixtureRoot 'data'
$logPath = Join-Path $fixtureRoot 'postgres.log'
$port = Get-Random -Minimum 20000 -Maximum 45000
$postgresBin = Get-ChildItem -LiteralPath 'C:\Program Files\PostgreSQL' -Directory -ErrorAction Stop | Sort-Object Name -Descending | ForEach-Object { Join-Path $_.FullName 'bin' } | Where-Object { Test-Path -LiteralPath (Join-Path $_ 'initdb.exe') } | Select-Object -First 1
if (-not $postgresBin) { throw 'A native PostgreSQL installation was not found.' }
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
if (-not ([IO.Path]::GetFullPath($fixtureRoot)).StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Maintenance fixture escaped the system temp directory.' }
$postgresRunning = $false

try {
    New-Item -ItemType Directory -Path $fixtureRoot | Out-Null
    & (Join-Path $postgresBin 'initdb.exe') -D $dataRoot -U iot_edge_acceptance_admin -A trust --no-locale --encoding=UTF8 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataRoot -l $logPath -o "-p $port -h 127.0.0.1" -w start
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL fixture failed to start' }
    $postgresRunning = $true
    & (Join-Path $postgresBin 'psql.exe') -h 127.0.0.1 -p $port -U iot_edge_acceptance_admin -d postgres -v ON_ERROR_STOP=1 -c 'CREATE ROLE iot_edge_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'dedicated role creation failed' }
    & (Join-Path $postgresBin 'createdb.exe') -h 127.0.0.1 -p $port -U iot_edge_acceptance_admin -O iot_edge_runtime iot_edge_maintenance_acceptance
    if ($LASTEXITCODE -ne 0) { throw 'dedicated database creation failed' }
    $env:TEST_DATABASE_URL = "postgres://iot_edge_runtime@127.0.0.1:$port/iot_edge_maintenance_acceptance?sslmode=disable"
    $env:TEST_PG_DUMP = Join-Path $postgresBin 'pg_dump.exe'
    $env:TEST_PG_RESTORE = Join-Path $postgresBin 'pg_restore.exe'
    Push-Location $backendRoot
    try { go test ./internal/dbmaintenance -run TestBackupRestoreDedicatedDatabaseIntegration -count=1 -v }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'maintenance integration acceptance failed' }
    [ordered]@{ measured_at=[DateTimeOffset]::Now.ToString('o'); native_windows=$true; disposable_postgresql='passed'; dedicated_role_owner='passed'; backup_checksum='passed'; restore='passed'; failures=0 } | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $evidencePath -Encoding utf8
}
finally {
    Remove-Item Env:TEST_DATABASE_URL,Env:TEST_PG_DUMP,Env:TEST_PG_RESTORE -ErrorAction SilentlyContinue
    if ($postgresRunning -and (Test-Path -LiteralPath $dataRoot)) { & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataRoot -m immediate -w stop | Out-Null }
    if ((Test-Path -LiteralPath $fixtureRoot) -and ([IO.Path]::GetFullPath($fixtureRoot)).StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { [IO.Directory]::Delete([IO.Path]::GetFullPath($fixtureRoot), $true) }
}
