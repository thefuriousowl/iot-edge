[CmdletBinding()]
param(
    [string]$Version = '0.2.0-rc.1',
    [switch]$SkipValidation
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$backendRoot = Join-Path $repositoryRoot 'backend'
$frontendRoot = Join-Path $repositoryRoot 'frontend'
$releaseRoot = Join-Path $repositoryRoot 'dist\release'
$stageRoot = Join-Path $releaseRoot "iot-edge-$Version-windows-amd64"
$archivePath = "$stageRoot.zip"
$manifestPath = Join-Path $stageRoot 'release-manifest.json'
$evidencePaths = [ordered]@{
    workload = Join-Path $repositoryRoot 'docs\performance\windows-workload-baseline.local.json'
    fault_soak = Join-Path $repositoryRoot 'docs\performance\windows-fault-soak.local.json'
    security = Join-Path $repositoryRoot 'docs\performance\windows-security-gates.local.json'
}
if ($Version -notmatch '^0\.2\.0-rc\.[1-9][0-9]*$') { throw 'Version must be a v0.2 release-candidate identifier.' }

function Invoke-Checked([scriptblock]$Command, [string]$Failure) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$Failure (exit code $LASTEXITCODE)" }
}

foreach ($entry in $evidencePaths.GetEnumerator()) {
    if (-not (Test-Path -LiteralPath $entry.Value)) { throw "Required qualification evidence is missing: $($entry.Key)" }
    $evidence = Get-Content -LiteralPath $entry.Value -Raw | ConvertFrom-Json
    $passing = switch ($entry.Key) {
        'workload' { [int]$evidence.backend.configured_tags -eq 500 -and [int]$evidence.backend.active_tags -eq 100 -and [int]$evidence.browser.clients -eq 10 }
        'fault_soak' { [int]$evidence.failures -eq 0 -and [int]$evidence.cycles -gt 0 -and [int]$evidence.postgres_restarts -gt 0 }
        'security' { [int]$evidence.failures -eq 0 -and $evidence.focused_security_contracts -eq 'passed' -and $evidence.gosec -eq 'passed' }
        default { $false }
    }
    if (-not $passing) { throw "Qualification evidence is not passing: $($entry.Key)" }
}

if (-not $SkipValidation) {
    Push-Location $backendRoot
    try {
        Invoke-Checked { go test ./... } 'backend tests failed'
        Invoke-Checked { go vet ./... } 'go vet failed'
        $tidyDiff = & go mod tidy -diff 2>&1
        if ($LASTEXITCODE -ne 0) {
            if ($tidyDiff -match 'diff current/go.mod') { throw 'go mod tidy reported a go.mod difference' }
            $removed = @($tidyDiff | Where-Object { $_ -match '^-(?!--)' } | ForEach-Object { $_.Substring(1) })
            $added = @($tidyDiff | Where-Object { $_ -match '^\+(?!\+\+)' } | ForEach-Object { $_.Substring(1) })
            if ($removed.Count -ne $added.Count -or (Compare-Object $removed $added -SyncWindow 0)) { throw 'go mod tidy reported a dependency-content difference' }
        }
    }
    finally { Pop-Location }
    Push-Location $frontendRoot
    try {
        Invoke-Checked { pnpm test } 'frontend tests failed'
        Invoke-Checked { pnpm lint } 'frontend lint failed'
        Invoke-Checked { pnpm build } 'frontend build failed'
        Invoke-Checked { node acceptance/critical-browser-e2e.mjs } 'critical browser E2E failed'
    }
    finally { Pop-Location }
    & (Join-Path $PSScriptRoot 'test-security-windows.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'security gates failed' }
}

Push-Location $frontendRoot
try { Invoke-Checked { pnpm build } 'production frontend build failed' }
finally { Pop-Location }
& (Join-Path $PSScriptRoot 'sync-frontend-windows.ps1')
if ($LASTEXITCODE -ne 0) { throw 'frontend embed synchronization failed' }

if (Test-Path -LiteralPath $stageRoot) { [IO.Directory]::Delete([IO.Path]::GetFullPath($stageRoot), $true) }
if (Test-Path -LiteralPath $archivePath) { [IO.File]::Delete([IO.Path]::GetFullPath($archivePath)) }
New-Item -ItemType Directory -Path $stageRoot | Out-Null
try {
    $env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    Push-Location $backendRoot
    try {
        foreach ($commandName in @('server', 'migrate', 'reset-password', 'service', 'windows-config', 'backup', 'restore')) {
            $outputName = switch ($commandName) { 'server' { 'iot-edge-server.exe' } 'migrate' { 'iot-edge-migrate.exe' } 'reset-password' { 'iot-edge-reset-password.exe' } 'service' { 'iot-edge-service.exe' } 'windows-config' { 'iot-edge-config.exe' } 'backup' { 'iot-edge-backup.exe' } 'restore' { 'iot-edge-restore.exe' } }
            Invoke-Checked { go build -trimpath -ldflags "-s -w -X github.com/thefuriousowl/iot-edge/internal/buildinfo.Version=$Version" -o (Join-Path $stageRoot $outputName) "./cmd/$commandName" } "building $commandName failed"
        }
    }
    finally { Pop-Location }
}
finally {
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}

Copy-Item -LiteralPath (Join-Path $repositoryRoot 'README.md'), (Join-Path $repositoryRoot 'LICENSE') -Destination $stageRoot
Set-Content -LiteralPath (Join-Path $stageRoot 'VERSION') -Value $Version -Encoding ascii

$binaryMetadata = foreach ($name in @('iot-edge-server.exe', 'iot-edge-migrate.exe', 'iot-edge-reset-password.exe', 'iot-edge-service.exe', 'iot-edge-config.exe', 'iot-edge-backup.exe', 'iot-edge-restore.exe')) {
    $path = Join-Path $stageRoot $name
    $moduleInfo = (& go version -m $path 2>&1) -join "`n"
    if ($LASTEXITCODE -ne 0 -or $moduleInfo -notmatch 'GOOS=windows' -or $moduleInfo -notmatch 'GOARCH=amd64') { throw "$name is not a Windows amd64 Go binary" }
    [ordered]@{ name = $name; bytes = (Get-Item -LiteralPath $path).Length; sha256 = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() }
}
$frontendFiles = @(Get-ChildItem -LiteralPath (Join-Path $frontendRoot 'dist') -File -Recurse)
& (Join-Path $PSScriptRoot 'test-release-candidate-windows.ps1') -ReleaseDirectory $stageRoot -ExpectedVersion $Version
if ($LASTEXITCODE -ne 0) { throw 'compiled release runtime smoke failed' }
$manifest = [ordered]@{
    product = 'IoT Edge'
    version = $Version
    release_kind = 'pre-installer Windows product release candidate'
    target = 'windows/amd64'
    created_at = [DateTimeOffset]::Now.ToString('o')
    git_commit = (& git -C $repositoryRoot rev-parse HEAD).Trim()
    dirty_worktree_preserved = $true
    binaries = @($binaryMetadata)
    frontend = [ordered]@{ files = $frontendFiles.Count; bytes = ($frontendFiles | Measure-Object Length -Sum).Sum; delivery = 'embedded in iot-edge-server.exe' }
    qualification_evidence = [ordered]@{ workload = 'passed'; fault_soak = 'passed'; security = 'passed'; browser_e2e = 'passed'; compiled_runtime = 'passed on disposable PostgreSQL' }
    limitations = @('Not an installer.', 'Requires a dedicated PostgreSQL database and deployment configuration.')
}
$manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding utf8
$hashLines = Get-ChildItem -LiteralPath $stageRoot -File | Where-Object Name -NotIn @('SHA256SUMS') | Sort-Object Name | ForEach-Object { "{0}  {1}" -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $_.Name }
$hashLines | Set-Content -LiteralPath (Join-Path $stageRoot 'SHA256SUMS') -Encoding ascii
Compress-Archive -LiteralPath $stageRoot -DestinationPath $archivePath -CompressionLevel Optimal
if (-not (Test-Path -LiteralPath $archivePath)) { throw 'Release archive creation failed' }
$archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
Write-Output "Release candidate: $archivePath"
Write-Output "SHA256: $archiveHash"
