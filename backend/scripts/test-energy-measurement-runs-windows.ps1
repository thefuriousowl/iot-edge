[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$postgresBin = 'C:\Program Files\PostgreSQL\16\bin'
$temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$testRoot = [System.IO.Path]::GetFullPath((Join-Path $temporaryRoot ('iot-edge-energy-runs-' + [guid]::NewGuid().ToString('N'))))

if (-not $testRoot.StartsWith($temporaryRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw 'Temporary PostgreSQL path escaped the system temp root.'
}
foreach ($executable in @('initdb.exe', 'pg_ctl.exe', 'createdb.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $postgresBin $executable))) {
        throw "PostgreSQL executable is missing: $executable"
    }
}

New-Item -ItemType Directory -Path $testRoot | Out-Null
$dataDirectory = Join-Path $testRoot 'data'
$logFile = Join-Path $testRoot 'postgres.log'
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()

try {
    & (Join-Path $postgresBin 'initdb.exe') -D $dataDirectory -U iot_edge_test_admin -A trust --no-locale --encoding=UTF8
    if ($LASTEXITCODE -ne 0) { throw "initdb failed with exit code $LASTEXITCODE" }
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -l $logFile -o "-h 127.0.0.1 -p $port" -w start
    if ($LASTEXITCODE -ne 0) { throw "pg_ctl start failed with exit code $LASTEXITCODE" }
    & (Join-Path $postgresBin 'createdb.exe') -h 127.0.0.1 -p $port -U iot_edge_test_admin iot_edge_energy_run_test
    if ($LASTEXITCODE -ne 0) { throw "createdb failed with exit code $LASTEXITCODE" }

    $env:TEST_DATABASE_URL = "postgres://iot_edge_test_admin@127.0.0.1:$port/iot_edge_energy_run_test?sslmode=disable"
    go test ./migrations ./internal/plugin/energy/postgres -run 'TestEnergyMeasurementRunsMigrationConstraintsAndDown_Integration|TestMeasurementRunRepositoryAtomicLifecycle_Integration' -count=1 -v
    if ($LASTEXITCODE -ne 0) { throw "Energy measurement-run integration tests failed with exit code $LASTEXITCODE" }
    go test -race ./internal/plugin/energy/postgres -run TestMeasurementRunRepositoryAtomicLifecycle_Integration -count=1
    if ($LASTEXITCODE -ne 0) { throw "Energy measurement-run race test failed with exit code $LASTEXITCODE" }
}
finally {
    Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $dataDirectory) {
        & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -m fast -w stop
    }
    if ((Test-Path -LiteralPath $testRoot) -and $testRoot.StartsWith($temporaryRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
