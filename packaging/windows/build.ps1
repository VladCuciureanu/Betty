[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$Configuration = "Release",
    [string]$MicrosoftSignedDriverDir,
    [switch]$RequireSigning,
    [switch]$RequireMicrosoftDriverSignature
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$artifacts = Join-Path $PSScriptRoot "artifacts"
$payload = Join-Path $artifacts "payload"
$driverOutput = Join-Path $artifacts "driver"
$obj = Join-Path $artifacts "obj"
$msiOutput = Join-Path $artifacts "msi"
$msiVersion = $Version.TrimStart("v").Split("-")[0]
if ($msiVersion -notmatch '^\d+\.\d+\.\d+(\.\d+)?$') {
    throw "Version must be an MSI-compatible numeric version, for example 1.2.3"
}
$driverVersion = $msiVersion
while ($driverVersion.Split('.').Count -lt 4) { $driverVersion += ".0" }

New-Item -ItemType Directory -Force $payload, $driverOutput, $obj, $msiOutput | Out-Null

Push-Location $root
try {
    go test ./...
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $payload "bt.exe") ./cmd/bt
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $payload "bt-driver-airpods.exe") ./cmd/bt-driver-airpods
    go build -trimpath -ldflags "-s -w" -o (Join-Path $payload "bt-windows-setup.exe") ./cmd/bt-windows-setup
    Copy-Item (Join-Path $PSScriptRoot "airpods-manifest.json") (Join-Path $payload "airpods-manifest.json") -Force
} finally {
    Pop-Location
}

$driverProject = Join-Path $PSScriptRoot "driver\BettyAAP.vcxproj"
if ($MicrosoftSignedDriverDir) {
    foreach ($name in @("BettyAAP.inf", "BettyAAP.sys", "BettyAAP.cat")) {
        $source = Join-Path $MicrosoftSignedDriverDir $name
        if (-not (Test-Path $source)) { throw "Microsoft-signed driver package is missing $name" }
        Copy-Item $source (Join-Path $driverOutput $name) -Force
    }
} else {
    if ($RequireMicrosoftDriverSignature) {
        throw "A Microsoft Hardware Dev Center-signed driver package is required for a release MSI"
    }
    msbuild $driverProject /restore /m /p:Configuration=$Configuration /p:Platform=x64 /p:DriverVersion=$driverVersion /p:SolutionDir="$PSScriptRoot\"
    if ($LASTEXITCODE -ne 0) { throw "WDK driver build failed" }
}

function Invoke-Sign([string[]]$Files) {
    $pfxPath = $env:BETTY_SIGNING_PFX
    $temporaryPfx = $null
    if (-not $pfxPath -and $env:BETTY_SIGNING_PFX_BASE64) {
        $temporaryPfx = Join-Path $env:RUNNER_TEMP "betty-signing.pfx"
        [IO.File]::WriteAllBytes($temporaryPfx, [Convert]::FromBase64String($env:BETTY_SIGNING_PFX_BASE64))
        $pfxPath = $temporaryPfx
    }
    try {
        if ($pfxPath) {
            foreach ($file in $Files) {
                & $script:SignTool sign /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" /f $pfxPath /p $env:BETTY_SIGNING_PFX_PASSWORD $file
                if ($LASTEXITCODE -ne 0) { throw "Signing failed for $file" }
            }
            return
        }
        if ($env:BETTY_SIGNING_CERT_SHA1) {
            foreach ($file in $Files) {
                & $script:SignTool sign /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" /sha1 $env:BETTY_SIGNING_CERT_SHA1 /sm $file
                if ($LASTEXITCODE -ne 0) { throw "Signing failed for $file" }
            }
            return
        }
        if ($RequireSigning) { throw "Release signing credentials are required" }
        Write-Warning "Building unsigned artifacts; the kernel driver will not load on a normal Windows installation."
    } finally {
        if ($temporaryPfx) { Remove-Item -Force $temporaryPfx -ErrorAction SilentlyContinue }
    }
}

function Find-WindowsKitTool([string]$Name) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    $kitRoot = Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin"
    $match = Get-ChildItem $kitRoot -Filter $Name -Recurse -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match '\\x64\\' } |
        Sort-Object FullName -Descending |
        Select-Object -First 1
    if (-not $match) { throw "Could not find $Name in the Windows SDK/WDK" }
    return $match.FullName
}

$script:SignTool = Find-WindowsKitTool "signtool.exe"
Invoke-Sign @(
    (Join-Path $payload "bt.exe"),
    (Join-Path $payload "bt-driver-airpods.exe"),
    (Join-Path $payload "bt-windows-setup.exe")
)

if ($MicrosoftSignedDriverDir) {
    & $script:SignTool verify /kp /v /c (Join-Path $driverOutput "BettyAAP.cat") (Join-Path $driverOutput "BettyAAP.sys")
    if ($LASTEXITCODE -ne 0) { throw "Driver package does not have a valid Microsoft kernel-policy signature" }
}

$installerProject = Join-Path $PSScriptRoot "installer\Betty.Installer.wixproj"
dotnet build $installerProject -c $Configuration -p:Version=$msiVersion -p:PayloadDir=$payload -p:DriverDir=$driverOutput -p:OutputPath=$msiOutput
if ($LASTEXITCODE -ne 0) { throw "MSI build failed" }
$msi = Get-ChildItem $msiOutput -Filter "*.msi" -Recurse | Select-Object -First 1
if (-not $msi) { throw "MSI build completed without producing an MSI" }
Invoke-Sign @($msi.FullName)
Write-Output $msi.FullName
