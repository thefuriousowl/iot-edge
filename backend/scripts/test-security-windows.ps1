[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$scannerRoot = [IO.Path]::GetFullPath((Join-Path $temporaryRoot ('iot-edge-security-' + [guid]::NewGuid().ToString('N'))))
$artifactDirectory = Join-Path $repositoryRoot 'docs\performance'
$artifactPath = Join-Path $artifactDirectory 'windows-security-gates.local.json'
$versions = [ordered]@{ govulncheck = 'v1.7.0'; gosec = 'v2.28.0'; gitleaks = 'v8.18.0' }
if (-not $scannerRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Temporary scanner path escaped the system temp root.' }

function Invoke-Checked([scriptblock]$Command, [string]$Failure) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "$Failure (exit code $LASTEXITCODE)" }
}

$startedAt = [DateTimeOffset]::Now
New-Item -ItemType Directory -Path $scannerRoot | Out-Null
try {
    $env:GOBIN = $scannerRoot
    Invoke-Checked { go install "golang.org/x/vuln/cmd/govulncheck@$($versions.govulncheck)" } 'govulncheck installation failed'
    Invoke-Checked { go install "github.com/securego/gosec/v2/cmd/gosec@$($versions.gosec)" } 'gosec installation failed'

    $gitleaksArchive = Join-Path $scannerRoot 'gitleaks.zip'
    $gitleaksURL = "https://github.com/gitleaks/gitleaks/releases/download/$($versions.gitleaks)/gitleaks_8.18.0_windows_x64.zip"
    Invoke-WebRequest -UseBasicParsing -Uri $gitleaksURL -OutFile $gitleaksArchive
    Expand-Archive -LiteralPath $gitleaksArchive -DestinationPath $scannerRoot

    Push-Location (Join-Path $repositoryRoot 'backend')
    try {
        Invoke-Checked { go test ./internal/auth/http ./internal/credential/http ./internal/device/http ./internal/publisher ./internal/publisher/http -run 'Test(RequireAuth_|AuthHandler|CredentialHandler|DeviceHandler|HTTPDefinitionRejectsUnsafeAndUnboundedConfig|HTTPServerProber|PublisherHandler)' -count=1 } 'focused security contracts failed'
        Invoke-Checked { & (Join-Path $scannerRoot 'govulncheck.exe') ./... } 'govulncheck failed'
        Invoke-Checked { & (Join-Path $scannerRoot 'gosec.exe') -quiet '-exclude=G101,G115,G404' ./... } 'gosec failed'
    }
    finally { Pop-Location }

    Push-Location (Join-Path $repositoryRoot 'frontend')
    try { Invoke-Checked { pnpm audit --audit-level high } 'pnpm audit failed' }
    finally { Pop-Location }

    Push-Location $repositoryRoot
    try { Invoke-Checked { & (Join-Path $scannerRoot 'gitleaks.exe') detect --source . --redact --no-banner } 'gitleaks failed' }
    finally { Pop-Location }

    $completedAt = [DateTimeOffset]::Now
    New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
    [ordered]@{
        started_at = $startedAt.ToString('o')
        completed_at = $completedAt.ToString('o')
        elapsed_seconds = [math]::Round(($completedAt - $startedAt).TotalSeconds, 1)
        platform = 'native Windows'
        scanner_versions = $versions
        focused_security_contracts = 'passed'
        govulncheck = 'passed: no called vulnerabilities'
        gosec = 'passed'
        pnpm_audit_high = 'passed'
        gitleaks_redacted = 'passed'
        failures = 0
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $artifactPath -Encoding utf8
    Write-Output "Security evidence written to $artifactPath"
}
finally {
    Remove-Item Env:GOBIN -ErrorAction SilentlyContinue
    if ((Test-Path -LiteralPath $scannerRoot) -and $scannerRoot.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase)) {
        [IO.Directory]::Delete($scannerRoot, $true)
    }
}
