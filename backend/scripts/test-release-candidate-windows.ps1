[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$ReleaseDirectory,
    [string]$ExpectedVersion = '0.2.0-rc.1'
)

$ErrorActionPreference = 'Stop'
$postgresBin = 'C:\Program Files\PostgreSQL\16\bin'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$smokeRoot = [IO.Path]::GetFullPath((Join-Path $temporaryRoot ('iot-edge-rc-smoke-' + [guid]::NewGuid().ToString('N'))))
$releaseDirectory = [IO.Path]::GetFullPath($ReleaseDirectory)
$artifactPath = Join-Path $repositoryRoot 'docs\performance\windows-rc-smoke.local.json'
if (-not $smokeRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Temporary RC smoke path escaped the system temp root.' }
foreach ($path in @((Join-Path $releaseDirectory 'iot-edge-server.exe'), (Join-Path $releaseDirectory 'iot-edge-migrate.exe'))) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Release binary is missing: $([IO.Path]::GetFileName($path))" }
}
foreach ($executable in @('initdb.exe', 'pg_ctl.exe', 'createdb.exe', 'psql.exe')) {
    if (-not (Test-Path -LiteralPath (Join-Path $postgresBin $executable))) { throw "PostgreSQL executable is missing: $executable" }
}

function New-LoopbackPort {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $selected = ([Net.IPEndPoint]$listener.LocalEndpoint).Port; $listener.Stop()
    return $selected
}
$dataDirectory = Join-Path $smokeRoot 'data'
$postgresLog = Join-Path $smokeRoot 'postgres.log'
$databasePort = New-LoopbackPort
$apiPort = New-LoopbackPort
$postgresRunning = $false
$serverProcess = $null
$environmentNames = @('DATABASE_URL','JWT_SECRET','PORT','ENV','COOKIE_SECURE','CORS_ALLOW_ORIGINS','INTERNET_CHECK_ADDRESS','INTERNET_CHECK_TIMEOUT')
foreach ($name in $environmentNames) { if (Test-Path "Env:$name") { throw "RC smoke requires an isolated shell without $name" } }
New-Item -ItemType Directory -Path $smokeRoot | Out-Null
try {
    & (Join-Path $postgresBin 'initdb.exe') -D $dataDirectory -U iot_edge_rc_admin -A trust --no-locale --encoding=UTF8 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
    & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -l $postgresLog -o "-h 127.0.0.1 -p $databasePort" -w start
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL start failed' }; $postgresRunning = $true
    & (Join-Path $postgresBin 'psql.exe') -h 127.0.0.1 -p $databasePort -U iot_edge_rc_admin -d postgres -v ON_ERROR_STOP=1 -c 'CREATE ROLE iot_edge_rc_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Disposable runtime role creation failed' }
    & (Join-Path $postgresBin 'createdb.exe') -h 127.0.0.1 -p $databasePort -U iot_edge_rc_admin -O iot_edge_rc_runtime iot_edge_rc
    if ($LASTEXITCODE -ne 0) { throw 'Disposable database creation failed' }

    $env:DATABASE_URL = "postgres://iot_edge_rc_runtime@127.0.0.1:$databasePort/iot_edge_rc?sslmode=disable"
    $env:JWT_SECRET = 'disposable-rc-smoke-secret-with-at-least-thirty-two-characters'
    $env:PORT = [string]$apiPort; $env:ENV = 'development'; $env:COOKIE_SECURE = 'false'
    $env:CORS_ALLOW_ORIGINS = "http://127.0.0.1:$apiPort"
    $env:INTERNET_CHECK_ADDRESS = '127.0.0.1:9'; $env:INTERNET_CHECK_TIMEOUT = '100ms'
    & (Join-Path $releaseDirectory 'iot-edge-migrate.exe') | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Release migration binary failed against the disposable database' }

    $serverProcess = Start-Process -FilePath (Join-Path $releaseDirectory 'iot-edge-server.exe') -WorkingDirectory $releaseDirectory -WindowStyle Hidden -PassThru
    $deadline = [DateTimeOffset]::Now.AddSeconds(30); $response = $null
    while ([DateTimeOffset]::Now -lt $deadline -and -not $serverProcess.HasExited) {
        try { $response = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$apiPort/api/health" -TimeoutSec 2; break } catch { Start-Sleep -Milliseconds 200 }
    }
    if ($null -eq $response -or $response.StatusCode -ne 200) { throw 'Release server health endpoint did not become ready' }
    $health = $response.Content | ConvertFrom-Json
    if ($health.status -ne 'ok' -or $health.version -ne $ExpectedVersion) { throw 'Release server health version contract failed' }
    foreach ($header in @{'Cache-Control'='no-store'; 'X-Content-Type-Options'='nosniff'; 'X-Frame-Options'='DENY'; 'Referrer-Policy'='no-referrer'}.GetEnumerator()) {
        if ($response.Headers[$header.Key] -ne $header.Value) { throw "Release health security header failed: $($header.Key)" }
    }
    $spa = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$apiPort/plugins/energy-1/energy" -TimeoutSec 5
    if ($spa.StatusCode -ne 200 -or $spa.Content -notmatch '<div id="root"></div>' -or $spa.Headers['Cache-Control'] -ne 'no-store' -or -not $spa.Headers['Content-Security-Policy']) { throw 'Embedded SPA deep-link contract failed' }
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$apiPort/api/plugins/00000000-0000-0000-0000-000000000001/energy/stream" -TimeoutSec 5 | Out-Null
        throw 'Unauthenticated SSE route unexpectedly succeeded'
    }
    catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 401) { throw 'SSE API route was swallowed by the SPA fallback' }
    }
    $env:IOT_EDGE_BASE_URL = "http://127.0.0.1:$apiPort"
    Push-Location (Join-Path $repositoryRoot 'frontend')
    try { & node acceptance/embedded-runtime-browser.mjs; if ($LASTEXITCODE -ne 0) { throw 'Embedded runtime Edge smoke failed' } }
    finally { Pop-Location; Remove-Item Env:IOT_EDGE_BASE_URL -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($artifactPath)) -Force | Out-Null
    [ordered]@{ measured_at=[DateTimeOffset]::Now.ToString('o'); version=$ExpectedVersion; target='windows/amd64'; migration='passed on disposable PostgreSQL'; health='passed'; embedded_spa='passed'; sse_route_boundary='passed'; edge_browser='passed'; security_headers='passed'; failures=0 } | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $artifactPath -Encoding utf8
    Write-Output "Release runtime smoke evidence written to $artifactPath"
}
finally {
    if ($null -ne $serverProcess -and -not $serverProcess.HasExited) { $serverProcess.Kill(); $serverProcess.WaitForExit(5000) | Out-Null }
    if ($null -ne $serverProcess) { $serverProcess.Dispose() }
    if ($postgresRunning) { & (Join-Path $postgresBin 'pg_ctl.exe') -D $dataDirectory -m fast -w stop }
    foreach ($name in $environmentNames) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
    if ((Test-Path -LiteralPath $smokeRoot) -and $smokeRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { [IO.Directory]::Delete($smokeRoot, $true) }
}
