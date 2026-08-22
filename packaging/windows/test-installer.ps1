[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$BaseMsi,
    [string]$AdvancedMsi,
    [switch]$BaseOnly,
    [switch]$SkipDriver
)

$ErrorActionPreference = "Stop"
$baseMsiPath = (Resolve-Path $BaseMsi).Path
$advancedMsiPath = if ($BaseOnly) { $null } else {
    if (-not $AdvancedMsi) { throw "AdvancedMsi is required unless BaseOnly is set" }
    (Resolve-Path $AdvancedMsi).Path
}
$installDirectory = Join-Path $env:ProgramFiles "Betty"
$advancedDirectory = Join-Path $env:ProgramFiles "Betty Apple Audio Advanced"
$skipArgument = if ($SkipDriver) { "SKIPDRIVER=1" } else { "" }

function Invoke-Msi([string]$Arguments, [string]$LogName, [bool]$ExpectSuccess = $true) {
    $log = Join-Path $env:RUNNER_TEMP $LogName
    $process = Start-Process msiexec.exe -ArgumentList "$Arguments /qn /norestart /l*v `"$log`"" -Wait -PassThru
    $succeeded = $process.ExitCode -in @(0, 1641, 3010)
    if ($succeeded -ne $ExpectSuccess) {
        Get-Content $log -Tail 100 -ErrorAction SilentlyContinue
        throw "msiexec returned unexpected exit code $($process.ExitCode)"
    }
}

$baseInstalled = $false
$advancedInstalled = $false
try {
    Invoke-Msi "/i `"$baseMsiPath`"" "betty-base-install.log"
    $baseInstalled = $true
    $bt = Join-Path $installDirectory "bt.exe"
    if (-not (Test-Path $bt)) { throw "Base MSI did not install bt.exe" }
    & $bt --version
    $drivers = & $bt driver list
    if ($drivers -match 'official/apple-audio') { throw "Base MSI unexpectedly bundled the Apple Audio driver" }

    if ($BaseOnly) {
        Invoke-Msi "/x `"$baseMsiPath`"" "betty-base-uninstall.log"
        $baseInstalled = $false
        if (Test-Path $installDirectory) { throw "Base MSI uninstall left the Betty installation directory behind" }
        return
    }

    Invoke-Msi "/i `"$advancedMsiPath`" $skipArgument" "betty-advanced-install.log"
    $advancedInstalled = $true
    $drivers = & $bt driver list
    if ($drivers -notmatch 'official/apple-audio') { throw "Advanced MSI Apple Audio driver was not discovered" }
    if (-not $SkipDriver) {
        $published = Get-ItemPropertyValue "HKLM:\SOFTWARE\Betty" "AirPodsDriverPublishedName"
        if ($published -notmatch '^oem\d+\.inf$') { throw "Advanced MSI did not record the staged profile driver" }
    }

    Invoke-Msi "/x `"$baseMsiPath`"" "betty-base-blocked-uninstall.log" $false
    if (-not (Test-Path $bt)) { throw "Base MSI was removed while Advanced Support was installed" }

    Invoke-Msi "/x `"$advancedMsiPath`" $skipArgument" "betty-advanced-uninstall.log"
    $advancedInstalled = $false
    if (Test-Path $advancedDirectory) { throw "Advanced MSI uninstall left its installation directory behind" }
    if (Test-Path "HKLM:\SOFTWARE\Betty\AirPodsAdvanced") { throw "Advanced MSI uninstall left its marker behind" }
    if (-not $SkipDriver) {
        $published = Get-ItemPropertyValue "HKLM:\SOFTWARE\Betty" "AirPodsDriverPublishedName" -ErrorAction SilentlyContinue
        if ($published) { throw "Advanced MSI uninstall left the profile driver registration behind" }
    }

    Invoke-Msi "/x `"$baseMsiPath`"" "betty-base-uninstall.log"
    $baseInstalled = $false
} finally {
    if ($advancedInstalled) {
        Invoke-Msi "/x `"$advancedMsiPath`" $skipArgument" "betty-advanced-cleanup.log"
    }
    if ($baseInstalled) {
        Invoke-Msi "/x `"$baseMsiPath`"" "betty-base-cleanup.log"
    }
}

if (Test-Path $installDirectory) { throw "Base MSI uninstall left the Betty installation directory behind" }
