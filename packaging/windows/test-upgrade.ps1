[CmdletBinding()]
param(
    # The base MSI of an older and a newer version, in that order.
    [Parameter(Mandatory = $true)][string]$OldMsi,
    [Parameter(Mandatory = $true)][string]$NewMsi,
    [Parameter(Mandatory = $true)][string]$NewVersion
)

# Upgrades, repairs, refuses to downgrade, and uninstalls the base MSI,
# checking bt, its system PATH entry, and per-user driver state after each
# step. It needs an elevated shell and leaves Betty uninstalled.
$ErrorActionPreference = "Stop"
$oldPath = (Resolve-Path $OldMsi).Path
$newPath = (Resolve-Path $NewMsi).Path
$installDirectory = Join-Path $env:ProgramFiles "Betty"
$bt = Join-Path $installDirectory "bt.exe"
$logs = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }

function Invoke-Msi([string]$Arguments, [string]$LogName, [bool]$ExpectSuccess = $true) {
    $log = Join-Path $logs $LogName
    $process = Start-Process msiexec.exe -ArgumentList "$Arguments /qn /norestart /l*v `"$log`"" -Wait -PassThru
    $succeeded = $process.ExitCode -in @(0, 1641, 3010)
    if ($succeeded -ne $ExpectSuccess) {
        Get-Content $log -Tail 100 -ErrorAction SilentlyContinue
        throw "msiexec $Arguments returned unexpected exit code $($process.ExitCode)"
    }
}

function Get-MachinePathEntries {
    $value = (Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment").Path
    return @($value -split ';' | Where-Object { $_ } | ForEach-Object { $_.TrimEnd('\') })
}

function Assert-Installed([string]$Version, [string]$Step) {
    if (-not (Test-Path $bt)) { throw "$Step left no bt.exe" }
    $reported = (& $bt --version) -join " "
    if ($reported -notmatch [regex]::Escape($Version)) { throw "$Step installed bt $reported, want $Version" }
    $entries = @(Get-MachinePathEntries | Where-Object { $_ -eq $installDirectory.TrimEnd('\') })
    if ($entries.Count -ne 1) { throw "$Step left $($entries.Count) PATH entries for Betty, want 1" }
}

# Per-user state for a driver, as Betty writes it; no package may touch it.
$state = Join-Path $env:APPDATA "betty\drivers\ZXhhbXBsZS9zdGF0ZQ\storage.json"
New-Item -ItemType Directory -Force (Split-Path $state) | Out-Null
Set-Content -Path $state -Value '{"keys":"kept"}' -NoNewline

$installed = $false
try {
    Invoke-Msi "/i `"$oldPath`"" "betty-old-install.log"
    $installed = $true
    $oldVersion = (& $bt --version) -join " "

    Invoke-Msi "/i `"$newPath`"" "betty-upgrade.log"
    Assert-Installed $NewVersion "The upgrade"
    $products = @(Get-ChildItem "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" |
        Where-Object { (Get-ItemProperty $_.PSPath).DisplayName -eq "Betty Bluetooth CLI" })
    if ($products.Count -ne 1) { throw "The upgrade left $($products.Count) Betty products registered, want 1" }

    Remove-Item $bt -Force
    Invoke-Msi "/fa `"$newPath`"" "betty-repair.log"
    Assert-Installed $NewVersion "The repair"

    Invoke-Msi "/i `"$oldPath`"" "betty-downgrade.log" $false
    Assert-Installed $NewVersion "A refused downgrade"

    Invoke-Msi "/x `"$newPath`"" "betty-uninstall.log"
    $installed = $false
    if (Test-Path $installDirectory) { throw "The uninstall left $installDirectory behind" }
    if (Get-MachinePathEntries | Where-Object { $_ -eq $installDirectory.TrimEnd('\') }) { throw "The uninstall left Betty on the system PATH" }
    if ((Get-Content $state -Raw) -ne '{"keys":"kept"}') { throw "Per-user driver state did not survive the packages" }
    Write-Host "MSI upgrade from $oldVersion to $NewVersion, repair, downgrade refusal, and uninstall OK"
} finally {
    if ($installed) { Invoke-Msi "/x `"$newPath`"" "betty-cleanup.log" }
    Remove-Item (Split-Path $state) -Recurse -Force -ErrorAction SilentlyContinue
}
