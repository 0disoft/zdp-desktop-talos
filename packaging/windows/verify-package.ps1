[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ReceiptPath,

    [switch]$RequireSignature
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
    }
}

Write-Output "verified $($receipt.artifacts.Count) artifacts for Talos $($receipt.version)"
