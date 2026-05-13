param(
  [string]$Token = "",
  [string]$Server = "47.114.96.114",
  [int]$ServerPort = 9500,
  [int]$RemotePort = 9502,
  [int]$LocalPort = 22,
  [string]$LocalIP = "127.0.0.1",
  [string]$Name = "windows-ssh",
  [int]$AdminPort = 7400,
  [string]$FrpVersion = "0.68.1",
  [string]$GitHubProxy = "https://gh-proxy.com/"
)

$ErrorActionPreference = 'Stop'

function Write-Usage {
@"
Usage:
  powershell -ExecutionPolicy Bypass -File .\install-frpc-client.ps1

Typical bootstrap:
  `$u='https://install.cicy-ai.com/frp';
  `$p=Join-Path `$env:TEMP 'install-frpc-client.ps1';
  irm `$u -OutFile `$p; powershell -ExecutionPolicy Bypass -File `$p
"@
}

function Test-IsAdmin {
  $currentUser = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($currentUser)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Ensure-Admin {
  if (Test-IsAdmin) { return }
  if (-not $PSCommandPath) {
    throw 'Please run this script from a file so it can self-elevate.'
  }
  Write-Host 'Requesting Administrator permission for Windows service install...'
  $args = @('-ExecutionPolicy','Bypass','-File',"`"$PSCommandPath`"")
  if ($Token) { $args += @('-Token',"`"$Token`"") }
  $args += @('-Server',"`"$Server`"",'-ServerPort',$ServerPort.ToString(),'-RemotePort',$RemotePort.ToString(),'-LocalPort',$LocalPort.ToString(),'-LocalIP',"`"$LocalIP`"",'-Name',"`"$Name`"",'-AdminPort',$AdminPort.ToString(),'-FrpVersion',"`"$FrpVersion`"",'-GitHubProxy',"`"$GitHubProxy`"")
  Start-Process powershell -Verb RunAs -ArgumentList ($args -join ' ')
  exit 0
}

function Prompt-Token {
  if ($script:Token) { return }
  $script:Token = Read-Host 'Enter FRP token'
  if (-not $script:Token) {
    throw 'FRP token cannot be empty.'
  }
}

function Invoke-DownloadWithProxy {
  param(
    [Parameter(Mandatory=$true)][string]$SourceUrl,
    [Parameter(Mandatory=$true)][string]$Dest
  )
  $prefixes = @($GitHubProxy, 'https://ghfast.top/', 'https://ghproxy.net/', '')
  foreach ($prefix in $prefixes) {
    $url = if ($prefix) { "$prefix$SourceUrl" } else { $SourceUrl }
    try {
      Write-Host "download: $url"
      Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $Dest
      return
    } catch {
      Write-Warning $_.Exception.Message
    }
  }
  throw "failed to download $SourceUrl"
}

if ($args -contains '--help' -or $args -contains '-h') {
  Write-Usage
  exit 0
}

Prompt-Token
Ensure-Admin

$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
switch ($arch) {
  'Arm64' { $frpPlatform = 'windows_arm64'; $winswAsset = 'WinSW-x64.exe' }
  'X64'   { $frpPlatform = 'windows_amd64'; $winswAsset = 'WinSW-x64.exe' }
  default { throw "unsupported Windows arch: $arch" }
}

$BinDir = Join-Path $HOME '.local\bin'
$FrpDir = Join-Path $HOME '.local\frp'
$CfgDir = Join-Path $HOME 'cicy-ai\db'
$CfgFile = Join-Path $CfgDir 'frpc.toml'
$LegacyCfgFile = Join-Path $HOME '.config\frp\frpc.toml'
$TmpDir = Join-Path $env:TEMP ("frpc-install-" + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Force -Path $BinDir, $FrpDir, $CfgDir, $TmpDir
# Self-migrate: pull an existing legacy config forward on first install.
if (-not (Test-Path $CfgFile) -and (Test-Path $LegacyCfgFile)) {
  Move-Item -Path $LegacyCfgFile -Destination $CfgFile
  Write-Host "  migrated config: $LegacyCfgFile -> $CfgFile"
}

try {
  Write-Host '[1/5] install frpc'
  $archive = "frp_${FrpVersion}_${frpPlatform}.zip"
  $frpSource = "https://github.com/fatedier/frp/releases/download/v${FrpVersion}/${archive}"
  $archivePath = Join-Path $TmpDir $archive
  Invoke-DownloadWithProxy -SourceUrl $frpSource -Dest $archivePath
  Expand-Archive -Path $archivePath -DestinationPath $TmpDir -Force
  Copy-Item (Join-Path $TmpDir "frp_${FrpVersion}_${frpPlatform}\frpc.exe") (Join-Path $BinDir 'frpc.exe') -Force

  Write-Host '[2/5] write config'
  @"
serverAddr = "$Server"
serverPort = $ServerPort

auth.method = "token"
auth.token = "$Token"

webServer.addr = "127.0.0.1"
webServer.port = $AdminPort

[[proxies]]
name = "$Name"
type = "tcp"
localIP = "$LocalIP"
localPort = $LocalPort
remotePort = $RemotePort
"@ | Set-Content -Path $CfgFile -Encoding UTF8

  Write-Host '[3/5] install WinSW service wrapper'
  $winswSource = "https://github.com/winsw/winsw/releases/download/v2.12.0/${winswAsset}"
  $winswExe = Join-Path $FrpDir 'frpc-service.exe'
  Invoke-DownloadWithProxy -SourceUrl $winswSource -Dest $winswExe
  $winswXml = Join-Path $FrpDir 'frpc-service.xml'
  @"
<service>
  <id>frpc-cicy</id>
  <name>frpc-cicy</name>
  <description>FRP Client for CiCy</description>
  <executable>$([System.Security.SecurityElement]::Escape((Join-Path $BinDir 'frpc.exe')))</executable>
  <arguments>-c &quot;$([System.Security.SecurityElement]::Escape($CfgFile))&quot;</arguments>
  <logpath>$([System.Security.SecurityElement]::Escape((Join-Path $FrpDir 'service-logs')))</logpath>
  <log mode="roll" />
  <onfailure action="restart" delay="5 sec" />
</service>
"@ | Set-Content -Path $winswXml -Encoding UTF8

  Write-Host '[4/5] install and start Windows service'
  & $winswExe stop | Out-Null 2>$null
  & $winswExe uninstall | Out-Null 2>$null
  & $winswExe install
  & $winswExe start

  Write-Host '[5/5] verify'
  Start-Sleep -Seconds 2
  & (Join-Path $BinDir 'frpc.exe') status -c $CfgFile

  Write-Host ''
  Write-Host 'done'
  Write-Host "connect with: ssh -p $RemotePort <your-user>@$Server"
  Write-Host 'if OpenSSH Server is not enabled on Windows, enable it first'
}
finally {
  Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
