[CmdletBinding()]
param(
    [string]$ContractPath = (Join-Path $PSScriptRoot 'signing-host.json')
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-CapturedNative {
    param(
        [Parameter(Mandatory)]
        [string]$Executable,

        [string[]]$Arguments = @()
    )

    $output = & $Executable @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$Executable failed with exit code $LASTEXITCODE."
    }
    return ($output | Out-String).Trim()
}

if (-not $IsWindows) {
    throw 'The Talos signing host must run Windows.'
}
if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') {
    throw "The Talos signing host must be amd64, not $($env:PROCESSOR_ARCHITECTURE)."
}
if ($env:CI -ne 'true') {
    throw 'The signing host preflight is restricted to CI execution.'
}

$contract = [IO.File]::ReadAllText((Resolve-Path -LiteralPath $ContractPath).Path) | ConvertFrom-Json
if ($contract.schema -ne 'talos.windows-signing-host/1' -or $contract.architecture -ne 'amd64') {
    throw 'Unsupported signing-host contract.'
}

$goVersion = Invoke-CapturedNative -Executable 'go' -Arguments @('version')
if ($goVersion -notmatch "\bgo$([Regex]::Escape($contract.go_major_minor))\.") {
    throw "Go $($contract.go_major_minor).x is required; found: $goVersion"
}

$bunVersion = Invoke-CapturedNative -Executable 'bun' -Arguments @('--version')
if ($bunVersion -ne $contract.bun_version) {
    throw "Bun $($contract.bun_version) is required; found: $bunVersion"
}

$makeNsis = Get-Command 'makensis.exe' -ErrorAction SilentlyContinue
if ($null -eq $makeNsis) {
    throw 'makensis.exe was not found on the signing host.'
}
$nsisVersion = Invoke-CapturedNative -Executable $makeNsis.Source -Arguments @('/VERSION')
if ($nsisVersion -notmatch "^v?$($contract.nsis_major)\.") {
    throw "NSIS $($contract.nsis_major).x is required; found: $nsisVersion"
}

$signTool = $env:TALOS_SIGNTOOL_PATH
if ([string]::IsNullOrWhiteSpace($signTool)) {
    $signToolCommand = Get-Command 'signtool.exe' -ErrorAction SilentlyContinue
    if ($null -eq $signToolCommand) {
        throw 'signtool.exe was not found on the signing host.'
    }
    $signTool = $signToolCommand.Source
}
$signTool = (Resolve-Path -LiteralPath $signTool).Path

$thumbprint = $env:TALOS_SIGN_CERTIFICATE_SHA1
if ([string]::IsNullOrWhiteSpace($thumbprint) -or $thumbprint -notmatch '^[A-Fa-f0-9]{40}$') {
    throw 'TALOS_SIGN_CERTIFICATE_SHA1 must contain one 40-character certificate thumbprint.'
}
$certificatePath = "Cert:\CurrentUser\My\$thumbprint"
$certificate = Get-Item -LiteralPath $certificatePath -ErrorAction SilentlyContinue
if ($null -eq $certificate) {
    throw 'The configured signing certificate was not found in Cert:\CurrentUser\My.'
}
if (-not $certificate.HasPrivateKey) {
    throw 'The configured signing certificate has no accessible private key.'
}
$minimumExpiry = (Get-Date).ToUniversalTime().AddDays([int]$contract.certificate_min_validity_days)
if ($certificate.NotAfter.ToUniversalTime() -le $minimumExpiry) {
    throw "The signing certificate expires before the $($contract.certificate_min_validity_days)-day release safety window."
}
$codeSigningOid = '1.3.6.1.5.5.7.3.3'
$enhancedKeyUsages = @($certificate.EnhancedKeyUsageList | ForEach-Object { $_.ObjectId.Value })
if ($codeSigningOid -notin $enhancedKeyUsages) {
    throw 'The configured certificate is not valid for code signing.'
}

$timestampUrl = $env:TALOS_TIMESTAMP_URL
if ([string]::IsNullOrWhiteSpace($timestampUrl)) {
    $timestampUrl = 'https://timestamp.digicert.com'
}
$timestampUri = $null
if (-not [Uri]::TryCreate($timestampUrl, [UriKind]::Absolute, [ref]$timestampUri) -or $timestampUri.Scheme -ne 'https') {
    throw 'TALOS_TIMESTAMP_URL must be an absolute HTTPS URL.'
}

$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$gitStatus = @(& git -C $repositoryRoot status --porcelain=v1 --untracked-files=normal)
if ($LASTEXITCODE -ne 0 -or $gitStatus.Count -ne 0) {
    throw 'The signing host checkout must be a clean Git worktree.'
}

[ordered]@{
    schema = 'talos.windows-signing-host-check/1'
    architecture = 'amd64'
    go = $goVersion
    bun = $bunVersion
    nsis = $nsisVersion
    signtool = $signTool
    certificate_subject = $certificate.Subject
    certificate_not_after = $certificate.NotAfter.ToUniversalTime().ToString('O')
    timestamp_host = $timestampUri.Host
} | ConvertTo-Json -Depth 3
