[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Path
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$resolvedPath = (Resolve-Path -LiteralPath $Path).Path
$thumbprint = $env:TALOS_SIGN_CERTIFICATE_SHA1
if ([string]::IsNullOrWhiteSpace($thumbprint) -or $thumbprint -notmatch '^[A-Fa-f0-9]{40}$') {
    throw 'TALOS_SIGN_CERTIFICATE_SHA1 must contain one 40-character certificate thumbprint.'
}

$signTool = $env:TALOS_SIGNTOOL_PATH
if ([string]::IsNullOrWhiteSpace($signTool)) {
    $command = Get-Command 'signtool.exe' -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        throw 'signtool.exe was not found. Use a provisioned Windows signing host.'
    }
    $signTool = $command.Source
}
$signTool = (Resolve-Path -LiteralPath $signTool).Path

$timestampUrl = $env:TALOS_TIMESTAMP_URL
if ([string]::IsNullOrWhiteSpace($timestampUrl)) {
    $timestampUrl = 'https://timestamp.digicert.com'
}
if (-not [Uri]::IsWellFormedUriString($timestampUrl, [UriKind]::Absolute)) {
    throw 'TALOS_TIMESTAMP_URL must be an absolute URL.'
}

$signArguments = @(
    'sign',
    '/sha1', $thumbprint,
    '/fd', 'SHA256',
    '/td', 'SHA256',
    '/tr', $timestampUrl,
    '/v',
    $resolvedPath
)
& $signTool @signArguments
if ($LASTEXITCODE -ne 0) {
    throw "signtool sign failed with exit code $LASTEXITCODE."
}

$verifyArguments = @('verify', '/pa', '/all', '/v', $resolvedPath)
& $signTool @verifyArguments
if ($LASTEXITCODE -ne 0) {
    throw "signtool verify failed with exit code $LASTEXITCODE."
}
