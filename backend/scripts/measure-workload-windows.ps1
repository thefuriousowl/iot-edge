[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$postgresBin = 'C:\Program Files\PostgreSQL\16\bin'
$temporaryRoot = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$profileRoot = [System.IO.Path]::GetFullPath((Join-Path $temporaryRoot ('iot-edge-workload-' + [guid]::NewGuid().ToString('N'))))
$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$artifactDirectory = Join-Path $repositoryRoot 'docs\performance'
$artifactPath = Join-Path $artifactDirectory 'windows-workload-baseline.local.json'

if (-not $profileRoot.StartsWith($temporaryRoot, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'Temporary workload path escaped the system temp root.' }
foreach ($executable in @('initdb.exe', 'pg_ctl.exe', 'createdb.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $postgresBin $executable))) { throw "PostgreSQL executable is missing: $executable" }
}

New-Item -ItemType Directory -Path $profileRoot | Out-Null
$dataDirectory = Join-Path $profileRoot 'data'
$logFile = Join-Path $profileRoot 'postgres.log'
$backendCapture = Join-Path $profileRoot 'backend-profile.txt'
$frontendCapture = Join-Path $profileRoot 'frontend-build.txt'
$browserCapture = Join-Path $profileRoot 'browser-profile.txt'
$bootstrapCapture = Join-Path $profileRoot 'postgres-bootstrap.txt'
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start(); $port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()

try {
    & (Join-Path $postgresBin 'initdb.exe') -D $dataDirectory -U iot_edge_workload_admin -A trust --no-locale --encoding=UTF8 *> $bootstrapCapture
    if ($LASTEXITCODE -ne 0) { throw "initdb failed with exit code $LASTEXITCODE" }
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -l $logFile -o "-h 127.0.0.1 -p $port" -w start
    if ($LASTEXITCODE -ne 0) { throw "pg_ctl start failed with exit code $LASTEXITCODE" }
    & (Join-Path $postgresBin 'createdb.exe') -h 127.0.0.1 -p $port -U iot_edge_workload_admin iot_edge_workload *>> $bootstrapCapture
    if ($LASTEXITCODE -ne 0) { throw "createdb failed with exit code $LASTEXITCODE" }

    $env:TEST_DATABASE_URL = "postgres://iot_edge_workload_admin@127.0.0.1:$port/iot_edge_workload?sslmode=disable"
    $env:RUN_WORKLOAD_PROFILE_TESTS = '1'
    & go test ./internal/datalogger/postgres -run TestMeasuredWorkloadProfile_Integration -count=1 -v *> $backendCapture
    $backendOutput = Get-Content -LiteralPath $backendCapture -Raw
    if ($LASTEXITCODE -ne 0) { throw "backend workload profile failed:`n$backendOutput" }
    $backendLine = [regex]::Match($backendOutput, 'WORKLOAD (?<metrics>[^\r\n]+)').Groups['metrics'].Value
    if (-not $backendLine) { throw 'backend workload metrics were not emitted' }
    $backend = [ordered]@{}
    foreach ($pair in $backendLine.Split(' ')) {
        $parts = $pair.Split('=', 2)
        $numeric = 0L
        $backend[$parts[0]] = if ([long]::TryParse($parts[1], [ref]$numeric)) { $numeric } else { $parts[1] }
    }

    Push-Location (Join-Path $repositoryRoot 'frontend')
    try {
        $previousErrorAction = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        & pnpm build *> $frontendCapture
        $frontendExitCode = $LASTEXITCODE
        $ErrorActionPreference = $previousErrorAction
        if ($frontendExitCode -ne 0) { throw "frontend build failed with exit code $frontendExitCode" }
        $ErrorActionPreference = 'Continue'
        & node acceptance/dashboard-workload-profile.mjs *> $browserCapture
        $browserExitCode = $LASTEXITCODE
        $ErrorActionPreference = $previousErrorAction
        $browserOutput = Get-Content -LiteralPath $browserCapture -Raw
        if ($browserExitCode -ne 0) { throw "browser workload profile failed:`n$browserOutput" }
    }
    finally { $ErrorActionPreference = 'Stop'; Pop-Location }
    $browserMatch = [regex]::Match($browserOutput, 'WORKLOAD_BROWSER (?<metrics>\{[^\r\n]+\})')
    if (-not $browserMatch.Success) { throw 'browser workload metrics were not emitted' }

    New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
    $artifact = [ordered]@{
        measured_at = [DateTimeOffset]::Now.ToString('o')
        platform = [System.Environment]::OSVersion.VersionString
        processor_count = [System.Environment]::ProcessorCount
        backend = $backend
        browser = ($browserMatch.Groups['metrics'].Value | ConvertFrom-Json)
        scope = 'Disposable native-Windows PostgreSQL and loopback production frontend fixture; values are a baseline, not a capacity promise.'
    }
    $artifact | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $artifactPath -Encoding utf8
    Write-Output "Workload baseline written to $artifactPath"
    $artifact | ConvertTo-Json -Depth 5
}
finally {
    Remove-Item Env:TEST_DATABASE_URL -ErrorAction SilentlyContinue
    Remove-Item Env:RUN_WORKLOAD_PROFILE_TESTS -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $dataDirectory) {
        & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -m fast -w stop
    }
    if ((Test-Path -LiteralPath $profileRoot) -and $profileRoot.StartsWith($temporaryRoot, [System.StringComparison]::OrdinalIgnoreCase)) { Remove-Item -LiteralPath $profileRoot -Recurse -Force }
}
