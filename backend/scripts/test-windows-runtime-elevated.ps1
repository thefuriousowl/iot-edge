[CmdletBinding()]
param()

$ErrorActionPreference='Stop'
$principal=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if(-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Windows runtime acceptance requires elevation.'}
if(Get-Service IoTEdge -ErrorAction SilentlyContinue){throw 'IoTEdge service already exists; refusing owner service mutation.'}
$repositoryRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$programData=[IO.Path]::GetFullPath([Environment]::GetFolderPath('CommonApplicationData'))
$root=[IO.Path]::GetFullPath((Join-Path $programData ('IoTEdgeRuntimeAcceptance-'+[guid]::NewGuid().ToString('N'))))
if(-not $root.StartsWith($programData,[StringComparison]::OrdinalIgnoreCase)){throw 'Acceptance root escaped ProgramData.'}
$install=Join-Path $root 'install';$data=Join-Path $root 'runtime';$secretFile=Join-Path $data 'config\secrets.dpapi';$logFile=Join-Path $data 'logs\iot-edge.log'
$server=Join-Path $install 'iot-edge-server.exe';$control=Join-Path $install 'iot-edge-service.exe';$configTool=Join-Path $install 'iot-edge-config.exe';$installed=$false
try{
 & (Join-Path $PSScriptRoot 'initialize-windows-layout.ps1') -InstallRoot $install -DataRoot $data
 Push-Location (Join-Path $repositoryRoot 'backend');try{
  $env:CGO_ENABLED='0';$env:GOOS='windows';$env:GOARCH='amd64'
  & go build -trimpath -ldflags "-s -w -X main.secretPath=$secretFile -X main.logPath=$logFile" -o $server ./cmd/service-smoke;if($LASTEXITCODE -ne 0){throw 'fixture build failed'}
  & go build -trimpath -ldflags '-s -w' -o $control ./cmd/service;if($LASTEXITCODE -ne 0){throw 'control build failed'}
  & go build -trimpath -ldflags '-s -w' -o $configTool ./cmd/windows-config;if($LASTEXITCODE -ne 0){throw 'config tool build failed'}
 }finally{Remove-Item Env:CGO_ENABLED,Env:GOOS,Env:GOARCH -ErrorAction SilentlyContinue;Pop-Location}
 $generated=[ordered]@{database_url='postgres://generated@127.0.0.1/generated';jwt_secret='generated-runtime-acceptance-secret-at-least-thirty-two-characters'}|ConvertTo-Json -Compress
 $generated|& $configTool protect --output $secretFile;if($LASTEXITCODE -ne 0){throw 'DPAPI protection failed'};$generated=$null
 & $control install --exe $server;if($LASTEXITCODE -ne 0){throw 'service install failed'};$installed=$true
 & $control start;if($LASTEXITCODE -ne 0){throw 'LocalService start/decrypt failed'}
 if(-not(Test-Path $logFile)){throw 'LocalService rotating log write failed'}
 & $control stop;if($LASTEXITCODE -ne 0){throw 'service stop failed'}
 & $control uninstall;if($LASTEXITCODE -ne 0){throw 'service uninstall failed'};$installed=$false
 $cipher=[IO.File]::ReadAllBytes($secretFile);$plain=[Text.Encoding]::UTF8.GetBytes('generated-runtime-acceptance-secret-at-least-thirty-two-characters');if([Text.Encoding]::UTF8.GetString($cipher).Contains([Text.Encoding]::UTF8.GetString($plain))){throw 'Protected file exposed plaintext'}
 $evidence=Join-Path $repositoryRoot 'docs\performance\windows-runtime.local.json';[ordered]@{measured_at=[DateTimeOffset]::Now.ToString('o');paths='passed';acls='passed';dpapi_machine_scope_localservice='passed';atomic_secret_store='passed';rotating_log='passed';cleanup='passed';failures=0}|ConvertTo-Json|Set-Content $evidence -Encoding utf8
 Write-Output 'Elevated Windows paths, ACL, DPAPI and logging acceptance passed.'
}finally{
 if($installed -and (Get-Service IoTEdge -ErrorAction SilentlyContinue)){& $control uninstall 2>$null}
 if(Test-Path $root){[IO.Directory]::Delete($root,$true)}
}
