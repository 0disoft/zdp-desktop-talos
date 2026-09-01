[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ReceiptPath,

    [switch]$RequireSignature,

    [string]$ExpectedSignerThumbprint,

    [string]$ExpectedSourceCommit
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

$resolvedReceipt = (Resolve-Path -LiteralPath $ReceiptPath).Path
$receiptRoot = Split-Path -Parent $resolvedReceipt
$receiptItem = Get-Item -LiteralPath $resolvedReceipt
if ($receiptItem.PSIsContainer -or ($receiptItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Package receipt must be a regular file.'
}
$receipt = [IO.File]::ReadAllText($resolvedReceipt) | ConvertFrom-Json
Assert-ExactProperties -Value $receipt -Expected @(
    'schema',
    'version',
    'architecture',
    'source_commit',
    'signature_required',
    'release_probe_schema',
    'artifacts'
) -Label 'Package receipt'
if ($receipt.schema -ne 'talos.windows-package-receipt/1') {
    throw "Unsupported package receipt schema: $($receipt.schema)"
}
if ($receipt.version -notmatch '^\d+\.\d+\.\d+$' -or $receipt.architecture -ne 'amd64') {
    throw 'Package receipt has an invalid version or architecture.'
}
if ($receipt.source_commit -notmatch '^[a-f0-9]{40}$') {
    throw 'Package receipt has an invalid source commit.'
}
if ($receipt.signature_required -isnot [bool] -or $receipt.release_probe_schema -ne 'talos.release-probe/1') {
    throw 'Package receipt has an invalid signing or release-probe contract.'
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedSourceCommit)) {
    if ($ExpectedSourceCommit -notmatch '^[a-fA-F0-9]{40}$' -or $receipt.source_commit -ne $ExpectedSourceCommit.ToLowerInvariant()) {
        throw 'Package receipt source commit does not match the expected workflow commit.'
    }
}
if ($RequireSignature -and -not $receipt.signature_required) {
    throw 'Signed package verification requires a signature-required receipt.'
}

$expectedArtifactNames = @(
    'talos-desktop.exe',
    'talos-worker.exe',
    'talosctl.exe',
    'talos-release-probe.exe',
    "talos-agent-$($receipt.version)-windows-amd64-setup.exe"
)
$artifacts = @($receipt.artifacts)
if ($artifacts.Count -ne $expectedArtifactNames.Count) {
    throw "Package receipt must contain exactly $($expectedArtifactNames.Count) artifacts."
}
$actualArtifactNames = @()
foreach ($artifact in $artifacts) {
    Assert-ExactProperties -Value $artifact -Expected @(
        'name',
        'sha256',
        'size',
        'signature_status',
        'signer_subject'
    ) -Label 'Package receipt artifact'
    if ($artifact.name -isnot [string] -or [IO.Path]::GetFileName($artifact.name) -ne $artifact.name) {
        throw 'Package receipt artifact name is invalid.'
    }
    $artifactSize = 0L
    if ($artifact.size -is [bool] -or -not [long]::TryParse([string]$artifact.size, [ref]$artifactSize) -or $artifactSize -lt 1) {
        throw "Package receipt hash or size is invalid for $($artifact.name)."
    }
    if ($artifact.sha256 -notmatch '^[a-f0-9]{64}$') {
        throw "Package receipt hash or size is invalid for $($artifact.name)."
    }
    if ($RequireSignature -and ($artifact.signature_status -ne 'Valid' -or [string]::IsNullOrWhiteSpace($artifact.signer_subject))) {
        throw "Package receipt signature metadata is invalid for $($artifact.name)."
    }
    $actualArtifactNames += $artifact.name
}
if (@($actualArtifactNames | Select-Object -Unique).Count -ne $actualArtifactNames.Count -or @(Compare-Object -ReferenceObject ($expectedArtifactNames | Sort-Object) -DifferenceObject ($actualArtifactNames | Sort-Object)).Count -ne 0) {
    throw 'Package receipt artifact identities are incomplete, duplicated, or unexpected.'
}

if ($RequireSignature -and [string]::IsNullOrWhiteSpace($ExpectedSignerThumbprint)) {
    $ExpectedSignerThumbprint = $env:TALOS_EXPECTED_SIGNER_SHA1
    if ([string]::IsNullOrWhiteSpace($ExpectedSignerThumbprint)) {
        $ExpectedSignerThumbprint = $env:TALOS_SIGN_CERTIFICATE_SHA1
    }
}
if ($RequireSignature -and $ExpectedSignerThumbprint -notmatch '^[A-Fa-f0-9]{40}$') {
    throw 'An expected 40-character signer thumbprint is required for signed package verification.'
}

foreach ($artifact in $artifacts) {
    $artifactPath = Join-Path $receiptRoot $artifact.name
    $resolvedArtifact = (Resolve-Path -LiteralPath $artifactPath).Path
    if ((Split-Path -Parent $resolvedArtifact) -ne $receiptRoot) {
        throw "Artifact escaped the receipt directory: $($artifact.name)"
    }
    $artifactItem = Get-Item -LiteralPath $resolvedArtifact
    if ($artifactItem.PSIsContainer -or ($artifactItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Package artifact must be a regular file: $($artifact.name)"
    }
    $hashAlgorithm = [Security.Cryptography.SHA256]::Create()
    $artifactStream = [IO.File]::OpenRead($resolvedArtifact)
    try {
        $actualHash = ([BitConverter]::ToString($hashAlgorithm.ComputeHash($artifactStream))).Replace('-', '').ToLowerInvariant()
    }
    finally {
        $artifactStream.Dispose()
        $hashAlgorithm.Dispose()
    }
    if ($actualHash -ne $artifact.sha256) {
        throw "SHA-256 mismatch for $($artifact.name)."
    }
    if ((Get-Item -LiteralPath $resolvedArtifact).Length -ne $artifact.size) {
        throw "Size mismatch for $($artifact.name)."
    }
    if ($RequireSignature) {
        $signature = Get-AuthenticodeSignature -LiteralPath $resolvedArtifact
        if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
            throw "Authenticode verification failed for $($artifact.name): $($signature.Status)"
        }
        if ($null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ne $ExpectedSignerThumbprint) {
            throw "Unexpected Authenticode signer for $($artifact.name)."
        }
    }
}

Write-Output "verified $($artifacts.Count) artifacts for Talos $($receipt.version)"
