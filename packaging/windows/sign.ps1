[CmdletBinding()]
param([Parameter(Mandatory = $true)][string[]]$Files)

$ErrorActionPreference = "Stop"
$signTool = Get-Command signtool.exe -ErrorAction SilentlyContinue
if (-not $signTool) {
    $signTool = Get-ChildItem (Join-Path ${env:ProgramFiles(x86)} "Windows Kits\10\bin") -Filter signtool.exe -Recurse |
        Where-Object { $_.FullName -match '\\x64\\' } | Sort-Object FullName -Descending | Select-Object -First 1
}
if (-not $signTool) { throw "signtool.exe was not found" }
$signToolPath = if ($signTool.Source) { $signTool.Source } else { $signTool.FullName }

$pfx = $env:BETTY_SIGNING_PFX
$temporary = $null
if (-not $pfx -and $env:BETTY_SIGNING_PFX_BASE64) {
    $temporary = Join-Path $env:RUNNER_TEMP "betty-signing.pfx"
    [IO.File]::WriteAllBytes($temporary, [Convert]::FromBase64String($env:BETTY_SIGNING_PFX_BASE64))
    $pfx = $temporary
}
try {
    foreach ($file in $Files) {
        if ($pfx) {
            & $signToolPath sign /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" /f $pfx /p $env:BETTY_SIGNING_PFX_PASSWORD $file
        } elseif ($env:BETTY_SIGNING_CERT_SHA1) {
            & $signToolPath sign /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" /sha1 $env:BETTY_SIGNING_CERT_SHA1 /sm $file
        } else {
            throw "No signing identity is configured"
        }
        if ($LASTEXITCODE -ne 0) { throw "Signing failed for $file" }
    }
} finally {
    if ($temporary) { Remove-Item -Force $temporary -ErrorAction SilentlyContinue }
}
