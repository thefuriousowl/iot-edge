[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$InstallRoot,
    [Parameter(Mandatory)][string]$DataRoot
)

$ErrorActionPreference = 'Stop'
$install = [IO.Path]::GetFullPath($InstallRoot)
$data = [IO.Path]::GetFullPath($DataRoot)
if (-not [IO.Path]::IsPathRooted($install) -or -not [IO.Path]::IsPathRooted($data) -or $install -eq $data) { throw 'Distinct absolute install and data roots are required.' }
foreach ($path in @($install,$data,(Join-Path $data 'config'),(Join-Path $data 'data'),(Join-Path $data 'logs'),(Join-Path $data 'backups'))) { New-Item -ItemType Directory -Path $path -Force | Out-Null }

& icacls.exe $install /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)RX' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Install-root ACL provisioning failed.' }
& icacls.exe $data /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)M' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Data-root ACL provisioning failed.' }
Write-Output 'Native Windows layout and ACLs initialized.'
