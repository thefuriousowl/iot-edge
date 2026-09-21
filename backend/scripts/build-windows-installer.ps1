[CmdletBinding()]
param(
    [string]$Version = '0.2.0-rc.1',
    [string]$CertificateThumbprint,
    [switch]$RequireSigning
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$backendRoot = Join-Path $repositoryRoot 'backend'
$sourceDir = Join-Path $repositoryRoot "dist\release\iot-edge-$Version-windows-amd64"
$outputDir = Join-Path $repositoryRoot 'dist\installer'
$intermediate = Join-Path $outputDir 'obj'
$license = Join-Path $backendRoot 'installer\license.rtf'
$msi = Join-Path $outputDir "iot-edge-$Version-windows-amd64.msi"
$bundle = Join-Path $outputDir "iot-edge-$Version-windows-amd64-setup.exe"
$productVersion = if ($Version -match '^([0-9]+)\.([0-9]+)\.([0-9]+)-rc\.([0-9]+)$') { "$($Matches[1]).$($Matches[2]).$($Matches[3]).$($Matches[4])" } else { throw 'Version must be a release-candidate identifier.' }

if (-not (Get-Command wix.exe -ErrorAction SilentlyContinue)) { throw 'WiX 5 CLI is required.' }
if ($RequireSigning -and -not $CertificateThumbprint) { throw 'Signing is required but no certificate thumbprint was supplied.' }
$signTool = $null
if ($CertificateThumbprint) {
    $certificate = Get-ChildItem Cert:\CurrentUser\My -CodeSigningCert -ErrorAction Stop | Where-Object { $_.Thumbprint -eq $CertificateThumbprint -and $_.HasPrivateKey -and $_.NotAfter -gt (Get-Date) } | Select-Object -First 1
    if (-not $certificate) { throw 'The requested valid code-signing certificate with private key was not found.' }
    $signTool = Get-ChildItem -LiteralPath 'C:\Program Files (x86)\Windows Kits\10\bin' -Filter signtool.exe -Recurse -ErrorAction Stop | Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
    if (-not $signTool) { throw 'signtool.exe was not found.' }
}
foreach ($name in @('iot-edge-server.exe','iot-edge-service.exe','iot-edge-config.exe','iot-edge-migrate.exe','iot-edge-reset-password.exe','iot-edge-backup.exe','iot-edge-restore.exe','README.md','LICENSE','VERSION','release-manifest.json','SHA256SUMS')) {
    if (-not (Test-Path -LiteralPath (Join-Path $sourceDir $name))) { throw "Release payload is missing: $name" }
}
New-Item -ItemType Directory -Path $outputDir,$intermediate -Force | Out-Null
Remove-Item -LiteralPath $msi,$bundle -Force -ErrorAction SilentlyContinue

wix build (Join-Path $backendRoot 'installer\product.wxs') -arch x64 -ext WixToolset.UI.wixext -d "SourceDir=$sourceDir" -d "ProductVersion=$productVersion" -d "LicenseRtf=$license" -intermediatefolder (Join-Path $intermediate 'msi') -out $msi
if ($LASTEXITCODE -ne 0) { throw 'MSI build failed.' }
if ($CertificateThumbprint) {
    & $signTool sign /sha1 $CertificateThumbprint /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 $msi
    if ($LASTEXITCODE -ne 0) { throw 'MSI signing failed.' }
}
wix build (Join-Path $backendRoot 'installer\bundle.wxs') -arch x64 -ext WixToolset.BootstrapperApplications.wixext -d "MsiPath=$msi" -d "ProductVersion=$productVersion" -d "LicenseRtf=$license" -intermediatefolder (Join-Path $intermediate 'bundle') -out $bundle
if ($LASTEXITCODE -ne 0) { throw 'Bootstrap build failed.' }

if ($CertificateThumbprint) {
    & $signTool sign /sha1 $CertificateThumbprint /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 $bundle
    if ($LASTEXITCODE -ne 0) { throw 'Bootstrap signing failed.' }
}

$artifacts = foreach ($path in @($msi,$bundle)) { [ordered]@{ name=[IO.Path]::GetFileName($path); bytes=(Get-Item $path).Length; sha256=(Get-FileHash $path -Algorithm SHA256).Hash.ToLowerInvariant(); signed=[bool]$CertificateThumbprint } }
$artifacts | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath (Join-Path $outputDir 'installer-manifest.json') -Encoding utf8
$artifacts | Format-Table -AutoSize
