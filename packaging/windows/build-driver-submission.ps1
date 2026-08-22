[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$artifacts = Join-Path $PSScriptRoot "artifacts"
$driverOutput = Join-Path $artifacts "driver"
$submission = Join-Path $artifacts "submission"
New-Item -ItemType Directory -Force $driverOutput, $submission | Out-Null
$driverVersion = $Version.TrimStart('v').Split('-')[0]
while ($driverVersion.Split('.').Count -lt 4) { $driverVersion += ".0" }

msbuild (Join-Path $PSScriptRoot "driver\BettyAAP.vcxproj") /restore /m /p:Configuration=Release /p:Platform=x64 /p:DriverVersion=$driverVersion /p:SolutionDir="$PSScriptRoot\"
if ($LASTEXITCODE -ne 0) { throw "WDK driver build failed" }

$cab = Join-Path $submission "BettyAAP-$($Version.TrimStart('v')).cab"
$ddf = Join-Path $submission "BettyAAP.ddf"
$lines = @(
    ".OPTION EXPLICIT",
    ".Set CabinetNameTemplate=$([IO.Path]::GetFileName($cab))",
    ".Set DiskDirectoryTemplate=$submission",
    ".Set CompressionType=MSZIP",
    ".Set Cabinet=on",
    ".Set Compress=on",
    "`"$(Join-Path $driverOutput 'BettyAAP.inf')`" BettyAAP.inf",
    "`"$(Join-Path $driverOutput 'BettyAAP.sys')`" BettyAAP.sys",
    "`"$(Join-Path $driverOutput 'BettyAAP.cat')`" BettyAAP.cat"
)
[IO.File]::WriteAllLines($ddf, $lines)
& makecab.exe /f $ddf
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $cab)) { throw "Driver submission CAB creation failed" }

if (-not ($env:BETTY_SIGNING_PFX -or $env:BETTY_SIGNING_PFX_BASE64 -or $env:BETTY_SIGNING_CERT_SHA1)) {
    throw "The Hardware Dev Center submission CAB must be signed with the certificate registered to Partner Center"
}
& (Join-Path $PSScriptRoot "sign.ps1") -Files $cab
Write-Output $cab
