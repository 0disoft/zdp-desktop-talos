[CmdletBinding()]
param(
    [string]$ContractPath = (Join-Path $PSScriptRoot 'upgrade-runner.json'),

    [Parameter(Mandatory)]
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-ExactProperties {
    param(
        [Parameter(Mandatory)]
        [object]$Value,

        [Parameter(Mandatory)]
        [string[]]$Expected,

        [Parameter(Mandatory)]
        [string]$Label
    )

    $actual = @($Value.PSObject.Properties.Name | Sort-Object)
    $wanted = @($Expected | Sort-Object)
    if ($actual.Count -ne $wanted.Count -or @(Compare-Object -ReferenceObject $wanted -DifferenceObject $actual).Count -ne 0) {
        throw "$Label has an unsupported field set."
    }
}

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

if (-not $IsWindows -or $env:PROCESSOR_ARCHITECTURE -ne 'AMD64') {
    throw 'The Talos upgrade runner must be Windows amd64.'
}
if (
    $env:CI -ne 'true' -or
    $env:GITHUB_ACTIONS -ne 'true' -or
    $env:TALOS_UPGRADE_SMOKE_EPHEMERAL -ne 'true' -or
    $env:RUNNER_OS -ne 'Windows' -or
    $env:RUNNER_ARCH -ne 'X64'
) {
    throw 'The Talos upgrade runner preflight requires the disposable GitHub Actions boundary.'
}
if ([string]::IsNullOrWhiteSpace($env:RUNNER_TEMP) -or [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    throw 'RUNNER_TEMP and LOCALAPPDATA are required.'
}

$contractPathResolved = (Resolve-Path -LiteralPath $ContractPath).Path
$contractItem = Get-Item -LiteralPath $contractPathResolved
if ($contractItem.PSIsContainer -or ($contractItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'The upgrade-runner contract must be a regular file.'
}
$contract = [IO.File]::ReadAllText($contractPathResolved) | ConvertFrom-Json
Assert-ExactProperties -Value $contract -Expected @(
    'schema',
    'architecture',
    'bun_version',
    'require_webview2',
    'forbid_code_signing_private_keys',
    'runner_labels'
) -Label 'Upgrade-runner contract'
if (
    $contract.schema -ne 'talos.windows-upgrade-runner/1' -or
    $contract.architecture -ne 'amd64' -or
    $contract.require_webview2 -ne $true -or
    $contract.forbid_code_signing_private_keys -ne $true -or
    (@($contract.runner_labels) -join ',') -ne 'self-hosted,windows,x64,talos-upgrade-smoke'
) {
    throw 'Unsupported upgrade-runner contract.'
}

$bunVersion = Invoke-CapturedNative -Executable 'bun' -Arguments @('--version')
if ($bunVersion -ne $contract.bun_version) {
    throw "Bun $($contract.bun_version) is required; found: $bunVersion"
}

$expectedSigner = $env:TALOS_EXPECTED_SIGNER_SHA1
if ([string]::IsNullOrWhiteSpace($expectedSigner) -or $expectedSigner -notmatch '^[A-Fa-f0-9]{40}$') {
    throw 'TALOS_EXPECTED_SIGNER_SHA1 must contain one 40-character public certificate thumbprint.'
}
$codeSigningOid = '1.3.6.1.5.5.7.3.3'
$privateCodeSigningCertificates = @()
foreach ($certificateStore in @('Cert:\CurrentUser\My', 'Cert:\LocalMachine\My')) {
    $privateCodeSigningCertificates += @(
        Get-ChildItem -LiteralPath $certificateStore | Where-Object {
            $_.HasPrivateKey -and $codeSigningOid -in @($_.EnhancedKeyUsageList | ForEach-Object { $_.ObjectId.Value })
        }
    )
}
if ($privateCodeSigningCertificates.Count -ne 0) {
    throw 'The upgrade runner must not expose any current-user or local-machine code-signing private key.'
}
$expectedCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$expectedSigner" -ErrorAction SilentlyContinue
if ($null -ne $expectedCertificate -and $expectedCertificate.HasPrivateKey) {
    throw 'The expected publisher private key is present on the upgrade runner.'
}

$webViewVersion = $null
foreach ($registryPath in @(
    'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}',
    'Registry::HKEY_CURRENT_USER\Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
)) {
    $candidate = Get-ItemPropertyValue -LiteralPath $registryPath -Name 'pv' -ErrorAction SilentlyContinue
    if (-not [string]::IsNullOrWhiteSpace($candidate)) {
        $webViewVersion = $candidate
        break
    }
}
if ([string]::IsNullOrWhiteSpace($webViewVersion)) {
    throw 'Microsoft Edge WebView2 Runtime is required on the upgrade runner.'
}

$installRoot = Join-Path $env:LOCALAPPDATA 'Programs\0disoft\Talos Agent'
$dataRoot = Join-Path $env:LOCALAPPDATA '0disoft\Talos Agent'
if (Test-Path -LiteralPath $installRoot) {
    throw 'The upgrade runner contains a pre-existing Talos installation.'
}
if (Test-Path -LiteralPath $dataRoot) {
    throw 'The upgrade runner contains pre-existing Talos data.'
}

$runnerTemp = (Resolve-Path -LiteralPath $env:RUNNER_TEMP).Path
$runnerTempItem = Get-Item -LiteralPath $runnerTemp
if (-not $runnerTempItem.PSIsContainer -or ($runnerTempItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'RUNNER_TEMP must be a real directory.'
}
$outputPathResolved = [IO.Path]::GetFullPath($OutputPath)
$outputRelative = [IO.Path]::GetRelativePath($runnerTemp, $outputPathResolved)
if (
    $outputRelative -eq '.' -or
    $outputRelative -eq '..' -or
    $outputRelative.StartsWith("..$([IO.Path]::DirectorySeparatorChar)", [StringComparison]::Ordinal) -or
    [IO.Path]::IsPathRooted($outputRelative)
) {
    throw 'The upgrade-runner report must be a file beneath RUNNER_TEMP.'
}
if (Test-Path -LiteralPath $outputPathResolved) {
    throw 'The upgrade-runner report already exists.'
}
$outputParent = Split-Path -Parent $outputPathResolved
if (-not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw 'The upgrade-runner report parent directory does not exist.'
}

$report = [ordered]@{
    schema = 'talos.windows-upgrade-runner-preflight/1'
    architecture = 'amd64'
    bun_version = $bunVersion
    signer_thumbprint_sha1 = $expectedSigner.ToLowerInvariant()
    webview2_ready = $true
    signing_private_keys_absent = $true
    installation_absent = $true
    data_absent = $true
}
$encoded = [Text.UTF8Encoding]::new($false).GetBytes(($report | ConvertTo-Json -Depth 3) + [Environment]::NewLine)
$stream = [IO.File]::Open($outputPathResolved, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try {
    $stream.Write($encoded, 0, $encoded.Length)
    $stream.Flush($true)
} finally {
    $stream.Dispose()
}

Write-Output 'Talos upgrade runner preflight passed.'
