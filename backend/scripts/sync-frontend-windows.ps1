[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$source = [IO.Path]::GetFullPath((Join-Path $repositoryRoot 'frontend\dist'))
$target = [IO.Path]::GetFullPath((Join-Path $repositoryRoot 'backend\internal\webui\assets'))
$expectedRoot = [IO.Path]::GetFullPath((Join-Path $repositoryRoot 'backend\internal\webui'))
if (-not $target.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Frontend embed target escaped internal/webui.' }
if (-not (Test-Path -LiteralPath (Join-Path $source 'index.html'))) { throw 'Production frontend is missing; run pnpm build first.' }
New-Item -ItemType Directory -Path $target -Force | Out-Null
Get-ChildItem -LiteralPath $target -Force | Where-Object Name -ne '_placeholder.txt' | ForEach-Object {
    if ($_.PSIsContainer) { [IO.Directory]::Delete($_.FullName, $true) } else { [IO.File]::Delete($_.FullName) }
}
Copy-Item -Path (Join-Path $source '*') -Destination $target -Recurse -Force
Write-Output "Production frontend synchronized for Go embedding: $target"
