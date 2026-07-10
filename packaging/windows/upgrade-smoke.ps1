[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$OldInstaller,

    [Parameter(Mandatory)]
    [string]$NewInstaller,

    [string]$ExpectedSignerThumbprint = $env:TALOS_EXPECTED_SIGNER_SHA1
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ($env:CI -ne 'true' -or [string]::IsNullOrWhiteSpace($env:RUNNER_TEMP)) {
    throw 'The upgrade smoke test is restricted to an ephemeral CI Windows account.'
}
if ($ExpectedSignerThumbprint -notmatch '^[A-Fa-f0-9]{40}$') {
    throw 'An expected 40-character signer thumbprint is required for upgrade smoke testing.'
}

$oldInstallerPath = (Resolve-Path -LiteralPath $OldInstaller).Path
$newInstallerPath = (Resolve-Path -LiteralPath $NewInstaller).Path
foreach ($installer in @($oldInstallerPath, $newInstallerPath)) {
    $signature = Get-AuthenticodeSignature -LiteralPath $installer
    if ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
        throw "Upgrade smoke input is not Authenticode-valid: $installer"
    }
    if ($null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ne $ExpectedSignerThumbprint) {
        throw "Upgrade smoke input has an unexpected publisher: $installer"
    }
}

$installRoot = Join-Path $env:LOCALAPPDATA 'Programs\0disoft\Talos Agent'
$vaultRoot = Join-Path $env:LOCALAPPDATA '0disoft\Talos Agent\Vaults\upgrade-smoke'
$sentinelPath = Join-Path $vaultRoot 'preserve-me.txt'
if (Test-Path -LiteralPath $installRoot) {
    throw "Refusing to overwrite a pre-existing Talos installation: $installRoot"
}

function Invoke-Installer {
    param([Parameter(Mandatory)][string]$Path)
    $process = Start-Process -FilePath $Path -ArgumentList @('/S') -Wait -PassThru -WindowStyle Hidden
    if ($process.ExitCode -ne 0) {
        throw "Installer failed with exit code $($process.ExitCode): $Path"
    }
}

try {
    Invoke-Installer -Path $oldInstallerPath
    foreach ($binary in @('talos-desktop.exe', 'talos-worker.exe', 'talosctl.exe')) {
        if (-not (Test-Path -LiteralPath (Join-Path $installRoot $binary))) {
            throw "Old installer did not install $binary."
        }
    }

    New-Item -ItemType Directory -Force -Path $vaultRoot | Out-Null
    [IO.File]::WriteAllText($sentinelPath, 'vault-data-must-survive-upgrade', [Text.UTF8Encoding]::new($false))

    Invoke-Installer -Path $newInstallerPath
    if (-not (Test-Path -LiteralPath $sentinelPath)) {
        throw 'Vault sentinel was removed during upgrade.'
    }

    $uninstaller = Join-Path $installRoot 'uninstall.exe'
    $process = Start-Process -FilePath $uninstaller -ArgumentList @('/S') -Wait -PassThru -WindowStyle Hidden
    if ($process.ExitCode -ne 0) {
        throw "Uninstaller failed with exit code $($process.ExitCode)."
    }
    if (-not (Test-Path -LiteralPath $sentinelPath)) {
        throw 'Vault sentinel was removed during uninstall.'
    }
} finally {
    if (Test-Path -LiteralPath $vaultRoot) {
        Remove-Item -LiteralPath $vaultRoot -Recurse -Force
    }
}
