[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version,

    [ValidateSet('amd64')]
    [string]$Architecture = 'amd64',

    [string]$OutputDirectory,

    [switch]$RequireSignature
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Native {
    param(
        [Parameter(Mandatory)]
        [string]$Executable,

        [Parameter(Mandatory)]
        [string[]]$Arguments
    )

    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Executable failed with exit code $LASTEXITCODE."
    }
}

function Assert-PathWithinRoot {
    param(
        [Parameter(Mandatory)]
        [string]$Candidate,

        [Parameter(Mandatory)]
        [string]$Root
    )

    $candidatePath = [IO.Path]::GetFullPath($Candidate)
    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $candidatePath.StartsWith($rootPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path is outside the repository root: $candidatePath"
    }
    return $candidatePath
}

$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$packageSourceRoot = Join-Path $repositoryRoot 'packaging\windows'
$artifactRoot = if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    Join-Path $repositoryRoot ".artifacts\windows\$Version"
} else {
    $OutputDirectory
}
$artifactRoot = Assert-PathWithinRoot -Candidate $artifactRoot -Root $repositoryRoot
$workingRoot = Assert-PathWithinRoot -Candidate (Join-Path $artifactRoot 'working') -Root $repositoryRoot
$generatedBuildRoot = Join-Path $workingRoot 'build'
$binaryRoot = Join-Path $workingRoot 'bin'
$installerPath = Join-Path $artifactRoot "talos-agent-$Version-windows-$Architecture-setup.exe"
$receiptPath = Join-Path $artifactRoot "talos-agent-$Version-windows-$Architecture-receipt.json"
$temporarySyso = Join-Path $repositoryRoot "wails_windows_$Architecture.syso"

$configText = [IO.File]::ReadAllText((Join-Path $packageSourceRoot 'config.yml'))
$versionPattern = '(?m)^\s+version:\s+[''"]?' + [Regex]::Escape($Version) + '[''"]?\s*$'
if ($configText -notmatch $versionPattern) {
    throw "Packaging config version does not match requested version $Version."
}

$gitStatus = @(& git -C $repositoryRoot status --porcelain=v1 --untracked-files=normal)
if ($LASTEXITCODE -ne 0) {
    throw 'Unable to inspect the source worktree before packaging.'
}
if ($gitStatus.Count -ne 0) {
    throw 'Release packaging requires a clean source worktree so the receipt commit identifies the built bytes.'
}

$makeNsis = Get-Command 'makensis.exe' -ErrorAction SilentlyContinue
if ($null -eq $makeNsis) {
    throw 'makensis.exe was not found. Install NSIS only on the provisioned packaging host.'
}

foreach ($finalArtifact in @($installerPath, $receiptPath)) {
    if (Test-Path -LiteralPath $finalArtifact) {
        throw "Refusing to overwrite an existing release artifact: $finalArtifact"
    }
}

if (Test-Path -LiteralPath $workingRoot) {
    Remove-Item -LiteralPath $workingRoot -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $generatedBuildRoot, $binaryRoot | Out-Null

$wailsPackage = 'github.com/wailsapp/wails/v3/cmd/wails3'
$signScript = Join-Path $packageSourceRoot 'sign-artifact.ps1'
$projectSource = Join-Path $packageSourceRoot 'project.nsi'

try {
    Copy-Item -LiteralPath (Join-Path $packageSourceRoot 'config.yml') -Destination (Join-Path $generatedBuildRoot 'config.yml')

    Push-Location $generatedBuildRoot
    try {
        Invoke-Native -Executable 'go' -Arguments @('run', $wailsPackage, 'generate', 'icons', '-example')
        Invoke-Native -Executable 'go' -Arguments @(
            'run', $wailsPackage, 'update', 'build-assets',
            '-name', 'zdp-desktop-talos',
            '-binaryname', 'talos-desktop',
            '-config', 'config.yml',
            '-dir', '.',
            '-silent'
        )
        Invoke-Native -Executable 'go' -Arguments @(
            'run', $wailsPackage, 'generate', 'icons',
            '-input', 'appicon.png',
            '-windowsfilename', 'windows/icon.ico'
        )
    } finally {
        Pop-Location
    }

    $nsisRoot = Join-Path $generatedBuildRoot 'windows\nsis'
    Copy-Item -LiteralPath $projectSource -Destination (Join-Path $nsisRoot 'project.nsi') -Force

    Invoke-Native -Executable 'bun' -Arguments @('--cwd', (Join-Path $repositoryRoot 'frontend'), 'run', 'build')
    Invoke-Native -Executable 'go' -Arguments @(
        'run', $wailsPackage, 'generate', 'syso',
        '-arch', $Architecture,
        '-icon', (Join-Path $generatedBuildRoot 'windows\icon.ico'),
        '-manifest', (Join-Path $generatedBuildRoot 'windows\wails.exe.manifest'),
        '-info', (Join-Path $generatedBuildRoot 'windows\info.json'),
        '-out', $temporarySyso
    )

    $desktopPath = Join-Path $binaryRoot 'talos-desktop.exe'
    $workerPath = Join-Path $binaryRoot 'talos-worker.exe'
    $cliPath = Join-Path $binaryRoot 'talosctl.exe'
    Push-Location $repositoryRoot
    try {
        Invoke-Native -Executable 'go' -Arguments @(
            'build', '-tags', 'production', '-trimpath', '-buildvcs=false',
            '-ldflags=-w -s -H windowsgui', '-o', $desktopPath, '.'
        )
        Invoke-Native -Executable 'go' -Arguments @(
            'build', '-trimpath', '-buildvcs=false', '-o', $workerPath, './cmd/talos-worker'
        )
        Invoke-Native -Executable 'go' -Arguments @(
            'build', '-trimpath', '-buildvcs=false', '-o', $cliPath, './cmd/talosctl'
        )
    } finally {
        Pop-Location
    }

    if ($RequireSignature) {
        foreach ($binary in @($desktopPath, $workerPath, $cliPath)) {
            & $signScript -Path $binary
        }
    }

    $makensisArguments = @(
        "-DARG_WAILS_AMD64_BINARY=$desktopPath",
        "-DARG_TALOS_WORKER_BINARY=$workerPath",
        "-DARG_TALOS_CLI_BINARY=$cliPath",
        "-DARG_TALOS_INSTALLER_OUTPUT=$installerPath"
    )
    if ($RequireSignature) {
        $makensisArguments += "-DARG_TALOS_SIGN_SCRIPT=$signScript"
    }
    $makensisArguments += (Join-Path $nsisRoot 'project.nsi')
    Invoke-Native -Executable $makeNsis.Source -Arguments $makensisArguments

    if ($RequireSignature) {
        & $signScript -Path $installerPath
    }

    $publishedDesktopPath = Join-Path $artifactRoot 'talos-desktop.exe'
    $publishedWorkerPath = Join-Path $artifactRoot 'talos-worker.exe'
    $publishedCliPath = Join-Path $artifactRoot 'talosctl.exe'
    Copy-Item -LiteralPath $desktopPath -Destination $publishedDesktopPath
    Copy-Item -LiteralPath $workerPath -Destination $publishedWorkerPath
    Copy-Item -LiteralPath $cliPath -Destination $publishedCliPath

    $gitCommit = (& git -C $repositoryRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $gitCommit -notmatch '^[a-f0-9]{40}$') {
        throw 'Unable to resolve the source commit for the package receipt.'
    }

    $artifacts = foreach ($artifact in @($publishedDesktopPath, $publishedWorkerPath, $publishedCliPath, $installerPath)) {
        $signature = Get-AuthenticodeSignature -LiteralPath $artifact
        [ordered]@{
            name = [IO.Path]::GetFileName($artifact)
            sha256 = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
            size = (Get-Item -LiteralPath $artifact).Length
            signature_status = $signature.Status.ToString()
            signer_subject = if ($null -eq $signature.SignerCertificate) { $null } else { $signature.SignerCertificate.Subject }
        }
    }

    $receipt = [ordered]@{
        schema = 'talos.windows-package-receipt/1'
        version = $Version
        architecture = $Architecture
        source_commit = $gitCommit
        signature_required = [bool]$RequireSignature
        artifacts = @($artifacts)
    }
    [IO.File]::WriteAllText(
        $receiptPath,
        ($receipt | ConvertTo-Json -Depth 5) + [Environment]::NewLine,
        [Text.UTF8Encoding]::new($false)
    )
} finally {
    if (Test-Path -LiteralPath $temporarySyso) {
        Remove-Item -LiteralPath $temporarySyso -Force
    }
}

Write-Output $receiptPath
