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

$resolvedReceipt = (Resolve-Path -LiteralPath $ReceiptPath).Path
$receiptRoot = Split-Path -Parent $resolvedReceipt
$receipt = [IO.File]::ReadAllText($resolvedReceipt) | ConvertFrom-Json
if ($receipt.schema -ne 'talos.windows-package-receipt/1') {
    throw "Unsupported package receipt schema: $($receipt.schema)"
}
if ($receipt.version -notmatch '^\d+\.\d+\.\d+$' -or $receipt.architecture -ne 'amd64') {
    throw 'Package receipt has an invalid version or architecture.'
}
if (-not [string]::IsNullOrWhiteSpace($ExpectedSourceCommit)) {
    if ($ExpectedSourceCommit -notmatch '^[a-fA-F0-9]{40}$' -or $receipt.source_commit -ne $ExpectedSourceCommit.ToLowerInvariant()) {
        throw 'Package receipt source commit does not match the expected workflow commit.'
    }
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

foreach ($artifact in $receipt.artifacts) {
    $artifactPath = Join-Path $receiptRoot $artifact.name
    $resolvedArtifact = (Resolve-Path -LiteralPath $artifactPath).Path
    if ((Split-Path -Parent $resolvedArtifact) -ne $receiptRoot) {
        throw "Artifact escaped the receipt directory: $($artifact.name)"
    }
    $actualHash = (Get-FileHash -LiteralPath $resolvedArtifact -Algorithm SHA256).Hash.ToLowerInvariant()
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

Write-Output "verified $($receipt.artifacts.Count) artifacts for Talos $($receipt.version)"
