[CmdletBinding()]
param(
    [ValidateRange(1, 60)][int]$DurationMinutes = 30,
    [ValidateRange(1, 300)][int]$CycleDelaySeconds = 5
)

$ErrorActionPreference = 'Stop'
$postgresBin = 'C:\Program Files\PostgreSQL\16\bin'
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$soakRoot = [IO.Path]::GetFullPath((Join-Path $temporaryRoot ('iot-edge-soak-' + [guid]::NewGuid().ToString('N'))))
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$artifactDirectory = Join-Path $repositoryRoot 'docs\performance'
$artifactPath = Join-Path $artifactDirectory 'windows-fault-soak.local.json'
if (-not $soakRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Temporary soak path escaped the system temp root.' }
foreach ($executable in @('initdb.exe', 'pg_ctl.exe', 'createdb.exe', 'psql.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $postgresBin $executable))) { throw "PostgreSQL executable is missing: $executable" }
}

New-Item -ItemType Directory -Path $soakRoot | Out-Null
$dataDirectory = Join-Path $soakRoot 'data'
$postgresLog = Join-Path $soakRoot 'postgres.log'
$testCapture = Join-Path $soakRoot 'fault-tests.txt'
$browserCapture = Join-Path $soakRoot 'browser.txt'
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
$postgresRunning = $false
$cycleDurations = [Collections.Generic.List[double]]::new()
$postgresWorkingSets = [Collections.Generic.List[long]]::new()
$postgresHandles = [Collections.Generic.List[int]]::new()
$harnessWorkingSets = [Collections.Generic.List[long]]::new()
$cycles = 0
$browserRuns = 0
$restartCount = 0

function Start-DisposablePostgres {
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -l $postgresLog -o "-h 127.0.0.1 -p $port" -w start
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL start failed with exit code $LASTEXITCODE" }
    $script:postgresRunning = $true
}
function Stop-DisposablePostgres {
    if (-not $script:postgresRunning) { return }
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -m fast -w stop
    if ($LASTEXITCODE -ne 0) { throw "PostgreSQL stop failed with exit code $LASTEXITCODE" }
    $script:postgresRunning = $false
}
function Measure-Resources {
    $connection = Get-NetTCPConnection -LocalAddress 127.0.0.1 -LocalPort $port -State Listen -ErrorAction SilentlyContinue
    if ($connection) {
        $process = Get-Process -Id $connection.OwningProcess
        $postgresWorkingSets.Add([long]$process.WorkingSet64)
        $postgresHandles.Add([int]$process.HandleCount)
    }
    $harnessWorkingSets.Add([long](Get-Process -Id $PID).WorkingSet64)
}
function Percentile([double[]]$Values, [double]$Fraction) {
    $ordered = @($Values | Sort-Object)
    return [math]::Round($ordered[[math]::Min($ordered.Count - 1, [math]::Ceiling($ordered.Count * $Fraction) - 1)], 1)
}

try {
    & (Join-Path $postgresBin 'initdb.exe') -D $dataDirectory -U iot_edge_soak_admin -A trust --no-locale --encoding=UTF8 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "initdb failed with exit code $LASTEXITCODE" }
    Start-DisposablePostgres
    & (Join-Path $postgresBin 'createdb.exe') -h 127.0.0.1 -p $port -U iot_edge_soak_admin iot_edge_soak
    if ($LASTEXITCODE -ne 0) { throw "createdb failed with exit code $LASTEXITCODE" }
    $env:TEST_DATABASE_URL = "postgres://iot_edge_soak_admin@127.0.0.1:$port/iot_edge_soak?sslmode=disable"

    Push-Location (Join-Path $repositoryRoot 'frontend')
    try { & pnpm build | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' } }
    finally { Pop-Location }

    $startedAt = [DateTimeOffset]::Now
    $deadline = $startedAt.AddMinutes($DurationMinutes)
    while ([DateTimeOffset]::Now -lt $deadline) {
        $cycleStarted = [Diagnostics.Stopwatch]::StartNew()
        $cycles++
        Push-Location (Join-Path $repositoryRoot 'backend')
        try {
            $previousErrorAction = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            & go test ./internal/vgateway ./internal/protocol/modbus ./internal/tag ./internal/datalogger ./internal/plugin ./internal/plugin/energy ./internal/publisher -run 'Test(VGatewayServiceConnectRetriesExistingClientAfterFailure|ModbusTCPClientConnectFailure|ModbusTCPClientDisconnectFailureClearsState|AcquisitionRuntimeIsolatesTagDecodeFailuresAndRecoversClosedStreams|RuntimeReportsRepositoryOutageAndRecovers|ManagerKeepsFailuresStableUntilExplicitRestart|EnergyRuntimePropagatesFeedFailures|LiveHubResetClosesOldGenerationAndAcceptsSameTimestamp|PublisherManagerReportsFactoryFailureAndSupportsExplicitRestart|PublisherManagerSurfacesTransportFailureAndLiveMetrics)$' -count=1 *> $testCapture
            $testExitCode = $LASTEXITCODE; $ErrorActionPreference = $previousErrorAction
            if ($testExitCode -ne 0) { throw "fault-cycle tests failed:`n$(Get-Content -LiteralPath $testCapture -Raw)" }
        }
        finally { $ErrorActionPreference = 'Stop'; Pop-Location }

        Stop-DisposablePostgres; Start-Sleep -Milliseconds 250; Start-DisposablePostgres; $restartCount++
        & (Join-Path $postgresBin 'psql.exe') -h 127.0.0.1 -p $port -U iot_edge_soak_admin -d iot_edge_soak -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL recovery probe failed' }

        if ($cycles -eq 1 -or $cycles % 10 -eq 0) {
            Push-Location (Join-Path $repositoryRoot 'frontend')
            try {
                $previousErrorAction = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
                & node acceptance/critical-browser-e2e.mjs *> $browserCapture
                $browserExitCode = $LASTEXITCODE; $ErrorActionPreference = $previousErrorAction
                if ($browserExitCode -ne 0) { throw "SSE/browser fault cycle failed:`n$(Get-Content -LiteralPath $browserCapture -Raw)" }
                $browserRuns++
            }
            finally { $ErrorActionPreference = 'Stop'; Pop-Location }
        }
        Measure-Resources
        $cycleStarted.Stop(); $cycleDurations.Add($cycleStarted.Elapsed.TotalMilliseconds)
        Write-Output ("soak cycle {0}: {1:n0} ms, PostgreSQL restarts={2}, browser runs={3}" -f $cycles, $cycleStarted.Elapsed.TotalMilliseconds, $restartCount, $browserRuns)
        $remaining = ($deadline - [DateTimeOffset]::Now).TotalSeconds
        if ($remaining -gt 0) { Start-Sleep -Seconds ([math]::Min($CycleDelaySeconds, [math]::Floor($remaining))) }
    }

    $completedAt = [DateTimeOffset]::Now
    $maxPostgresWorkingSet = ($postgresWorkingSets | Measure-Object -Maximum).Maximum
    $maxPostgresHandles = ($postgresHandles | Measure-Object -Maximum).Maximum
    $maxHarnessWorkingSet = ($harnessWorkingSets | Measure-Object -Maximum).Maximum
    if ($maxPostgresWorkingSet -gt 512MB -or $maxHarnessWorkingSet -gt 512MB -or $maxPostgresHandles -gt 2048) { throw 'soak resource gate exceeded' }
    New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
    $artifact = [ordered]@{
        started_at = $startedAt.ToString('o'); completed_at = $completedAt.ToString('o'); requested_minutes = $DurationMinutes
        elapsed_seconds = [math]::Round(($completedAt - $startedAt).TotalSeconds, 1); cycles = $cycles; postgres_restarts = $restartCount; browser_sse_runs = $browserRuns
        cycle_p50_ms = Percentile $cycleDurations.ToArray() 0.5; cycle_p95_ms = Percentile $cycleDurations.ToArray() 0.95
        postgres_working_set_min_bytes = ($postgresWorkingSets | Measure-Object -Minimum).Minimum; postgres_working_set_max_bytes = $maxPostgresWorkingSet
        postgres_handle_min = ($postgresHandles | Measure-Object -Minimum).Minimum; postgres_handle_max = $maxPostgresHandles
        harness_working_set_max_bytes = $maxHarnessWorkingSet; failures = 0
        scope = 'Disposable native-Windows PostgreSQL plus deterministic runtime/network/Modbus/SSE/Plugin/Publisher fault cycles.'
    }
    $artifact | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $artifactPath -Encoding utf8
    Write-Output "Fault/soak evidence written to $artifactPath"
    $artifact | ConvertTo-Json -Depth 4
}
finally {
    Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue
    if ($postgresRunning) { Stop-DisposablePostgres }
    if ((Test-Path -LiteralPath $soakRoot) -and $soakRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { [IO.Directory]::Delete($soakRoot, $true) }
}
