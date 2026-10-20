[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$Configuration = "Release",
    [string]$MicrosoftSignedDriverDir,
    [switch]$RequireSigning,
    [switch]$RequireMicrosoftDriverSignature,
    [switch]$BaseOnly
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
if (-not $BaseOnly -and -not $MicrosoftSignedDriverDir -and $driverVersion -eq "0.0.0.0") {
    # InfVerif rejects an all-zero DriverVer.
    throw "Version 0.0.0 cannot be stamped into the driver INF; use 0.0.1 or later"
}

New-Item -ItemType Directory -Force $payload, $driverOutput, $obj, $msiOutput | Out-Null
Get-ChildItem $msiOutput -Filter "*.msi" -Recurse -ErrorAction SilentlyContinue | Remove-Item -Force

Push-Location $root
try {
    go test ./...
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $payload "bt.exe") ./cmd/bt
    if (-not $BaseOnly) {
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $payload "bt-driver-apple-audio.exe") ./cmd/bt-driver-apple-audio
        go build -trimpath -ldflags "-s -w" -o (Join-Path $payload "bt-windows-setup.exe") ./cmd/bt-windows-setup
        Copy-Item (Join-Path $PSScriptRoot "apple-audio-manifest.json") (Join-Path $payload "apple-audio-manifest.json") -Force
    }
} finally {
    Pop-Location
}

if (-not $BaseOnly) {
    $driverProject = Join-Path $PSScriptRoot "driver\BettyAAP.vcxproj"
    if ($MicrosoftSignedDriverDir) {
        foreach ($name in @("BettyAAP.inf", "BettyAAP.sys", "BettyAAP.cat")) {
            $source = Join-Path $MicrosoftSignedDriverDir $name
            if (-not (Test-Path $source)) { throw "Microsoft-signed driver package is missing $name" }
            Copy-Item $source (Join-Path $driverOutput $name) -Force
        }
    } else {
        if ($RequireMicrosoftDriverSignature) {
            throw "A Microsoft Hardware Dev Center-signed driver package is required for the Advanced Support MSI"
        }
        # The WDK's InfVerif task needs a 64-bit MSBuild: the x64 WDK package
        # has no x86 InfVerif.dll. Prefer the amd64 MSBuild next to an x86 one.
        $msbuild = (Get-Command msbuild -ErrorAction Stop).Source
        $msbuild64 = Join-Path (Split-Path $msbuild) "amd64\MSBuild.exe"
        if ((Split-Path (Split-Path $msbuild) -Leaf) -notin @("amd64", "arm64") -and (Test-Path $msbuild64)) {
            $msbuild = $msbuild64
        }
        & $msbuild $driverProject /restore /m /p:Configuration=$Configuration /p:Platform=x64 /p:DriverVersion=$driverVersion /p:SolutionDir="$PSScriptRoot\"
        if ($LASTEXITCODE -ne 0) { throw "WDK driver build failed" }
        # The WDK writes the driver package, with its catalog, to a subfolder.
        $package = Join-Path $driverOutput "BettyAAP"
        foreach ($name in @("BettyAAP.inf", "BettyAAP.sys", "BettyAAP.cat")) {
            $source = Join-Path $package $name
            if (-not (Test-Path $source)) { throw "WDK driver package is missing $name" }
            Copy-Item $source (Join-Path $driverOutput $name) -Force
        }
    }
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
$executables = @((Join-Path $payload "bt.exe"))
if (-not $BaseOnly) {
    $executables += (Join-Path $payload "bt-driver-apple-audio.exe"), (Join-Path $payload "bt-windows-setup.exe")
}
Invoke-Sign $executables

if (-not $BaseOnly -and $MicrosoftSignedDriverDir) {
    & $script:SignTool verify /kp /v /c (Join-Path $driverOutput "BettyAAP.cat") (Join-Path $driverOutput "BettyAAP.sys")
    if ($LASTEXITCODE -ne 0) { throw "Driver package does not have a valid Microsoft kernel-policy signature" }
}

$installerProjects = @(
    (Join-Path $PSScriptRoot "installer\Betty.Installer.wixproj")
)
if (-not $BaseOnly) {
    $installerProjects += (Join-Path $PSScriptRoot "installer\Betty.AirPodsAdvanced.wixproj")
}
foreach ($installerProject in $installerProjects) {
    dotnet build $installerProject -c $Configuration -p:Version=$msiVersion -p:PayloadDir=$payload -p:DriverDir=$driverOutput -p:OutputPath=$msiOutput
    if ($LASTEXITCODE -ne 0) { throw "MSI build failed for $installerProject" }
}
$msis = @(Get-ChildItem $msiOutput -Filter "*.msi" -Recurse)
$expectedMSIs = if ($BaseOnly) { 1 } else { 2 }
if ($msis.Count -ne $expectedMSIs) { throw "MSI build expected $expectedMSIs package(s) but produced $($msis.Count)" }
Invoke-Sign @($msis.FullName)
$msis.FullName | Write-Output
