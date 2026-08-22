[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Msi,
    [switch]$SkipDriver
)

$ErrorActionPreference = "Stop"
$msiPath = (Resolve-Path $Msi).Path
$installDirectory = Join-Path $env:ProgramFiles "Betty"
$installLog = Join-Path $env:RUNNER_TEMP "betty-msi-install.log"
$uninstallLog = Join-Path $env:RUNNER_TEMP "betty-msi-uninstall.log"
$skipArgument = if ($SkipDriver) { "SKIPDRIVER=1" } else { "" }

function Invoke-Msi([string]$Arguments, [string]$Log) {
    $process = Start-Process msiexec.exe -ArgumentList "$Arguments /qn /norestart /l*v `"$Log`"" -Wait -PassThru
    if ($process.ExitCode -notin @(0, 1641, 3010)) {
        Get-Content $Log -Tail 100 -ErrorAction SilentlyContinue
        throw "msiexec failed with exit code $($process.ExitCode)"
    }
}

$installed = $false
try {
    Invoke-Msi "/i `"$msiPath`" $skipArgument" $installLog
    $installed = $true
    $bt = Join-Path $installDirectory "bt.exe"
    if (-not (Test-Path $bt)) { throw "MSI did not install bt.exe" }
    & $bt --version
    $drivers = & $bt driver list
    if ($drivers -notmatch 'official/airpods') { throw "Bundled AirPods driver was not discovered" }
    if (-not $SkipDriver) {
        $published = Get-ItemPropertyValue "HKLM:\SOFTWARE\Betty" "AirPodsDriverPublishedName"
        if ($published -notmatch '^oem\d+\.inf$') { throw "MSI did not record the staged profile driver" }
    }
} finally {
    if ($installed) {
        Invoke-Msi "/x `"$msiPath`" $skipArgument" $uninstallLog
    }
}

if (Test-Path $installDirectory) { throw "MSI uninstall left the Betty installation directory behind" }
if (-not $SkipDriver -and (Test-Path "HKLM:\SOFTWARE\Betty")) { throw "MSI uninstall left Betty machine state behind" }
