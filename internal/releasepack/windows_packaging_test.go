//go:build windows

package releasepack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestWindowsPackagingPowerShellParses(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	scripts := []string{
		"package.ps1",
		"sign-artifact.ps1",
		"verify-package.ps1",
		"verify-signing-host.ps1",
		"verify-upgrade-runner.ps1",
	}
	for _, name := range scripts {
		name := name
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "packaging", "windows", name)
			escapedPath := strings.ReplaceAll(path, `'`, `''`)
			parser := `$tokens=$null;$errors=$null;[System.Management.Automation.Language.Parser]::ParseFile('` + escapedPath + `',[ref]$tokens,[ref]$errors)|Out-Null;if($errors.Count -gt 0){$errors|ForEach-Object{[Console]::Error.WriteLine($_.Message)};exit 1}`
			command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", parser)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("PowerShell parser rejected %s: %v\n%s", name, err, output)
			}
		})
	}
}

func TestWindowsInstallerContract(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	project := readFile(t, filepath.Join(root, "packaging", "windows", "project.nsi"))
	required := []string{
		`!define WAILS_INSTALL_SCOPE "user"`,
		`!define REQUEST_EXECUTION_LEVEL "user"`,
		`File "/oname=talos-worker.exe" "${ARG_TALOS_WORKER_BINARY}"`,
		`File "/oname=talosctl.exe" "${ARG_TALOS_CLI_BINARY}"`,
		`Call Talos.CheckWebView2`,
		`!uninstfinalize`,
	}
	for _, expected := range required {
		if !strings.Contains(project, expected) {
			t.Errorf("project.nsi is missing %q", expected)
		}
	}
	forbidden := []string{
		`CRYPTPROTECT_LOCAL_MACHINE`,
		`RMDir /r "$LOCALAPPDATA\0disoft\Talos Agent`,
		`MicrosoftEdgeWebview2Setup.exe`,
	}
	for _, value := range forbidden {
		if strings.Contains(project, value) {
			t.Errorf("project.nsi contains forbidden packaging behavior %q", value)
		}
	}
}

func TestWindowsPackageRequiresCleanSourceProvenance(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	packager := readFile(t, filepath.Join(root, "packaging", "windows", "package.ps1"))
	for _, expected := range []string{
		"status --porcelain=v1 --untracked-files=normal",
		"Release packaging requires a clean source worktree",
		"source_commit = $gitCommit",
	} {
		if !strings.Contains(packager, expected) {
			t.Errorf("package.ps1 is missing provenance guard %q", expected)
		}
	}
}

func TestWindowsPackagePublishesSignedReleaseProbeOutsideInstaller(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	packager := readFile(t, filepath.Join(root, "packaging", "windows", "package.ps1"))
	for _, expected := range []string{
		"./cmd/talos-release-probe",
		"talos-release-probe.exe",
		"release_probe_schema = 'talos.release-probe/1'",
		"$desktopPath, $workerPath, $cliPath, $releaseProbePath",
	} {
		if !strings.Contains(packager, expected) {
			t.Errorf("package.ps1 is missing release-probe contract %q", expected)
		}
	}
	project := readFile(t, filepath.Join(root, "packaging", "windows", "project.nsi"))
	if strings.Contains(project, "talos-release-probe") {
		t.Fatal("release probe must not be installed on user machines")
	}
}

func TestWindowsPackageReceiptContractIsStrict(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	schemaContent := readFile(t, filepath.Join(root, "contracts", "jsonschema", "release", "v1", "windows-package-receipt.schema.json"))
	for _, expected := range []string{
		`"$schema": "https://json-schema.org/draft/2020-12/schema"`,
		`"additionalProperties": false`,
		`"talos.windows-package-receipt/1"`,
		`"talos.release-probe/1"`,
		`"minItems": 5`,
		`"maxItems": 5`,
		`"talos-release-probe.exe"`,
	} {
		if !strings.Contains(schemaContent, expected) {
			t.Errorf("Windows package receipt schema is missing %q", expected)
		}
	}
	for _, fixture := range []string{
		"valid-windows-package-receipt.json",
		"invalid-windows-package-receipt-duplicate-artifact.json",
	} {
		content := readFile(t, filepath.Join(root, "contracts", "fixtures", "release", "v1", fixture))
		if !strings.Contains(content, `"schema": "talos.windows-package-receipt/1"`) {
			t.Errorf("%s does not declare the package receipt schema", fixture)
		}
	}

	verifier := readFile(t, filepath.Join(root, "packaging", "windows", "verify-package.ps1"))
	for _, expected := range []string{
		"Assert-ExactProperties",
		"release_probe_schema",
		"expectedArtifactNames",
		"Select-Object -Unique",
		"FileAttributes]::ReparsePoint",
		"Security.Cryptography.SHA256",
		"signature-required receipt",
	} {
		if !strings.Contains(verifier, expected) {
			t.Errorf("verify-package.ps1 is missing strict receipt guard %q", expected)
		}
	}
}

func TestWindowsPackageVerifierAcceptsOnlyTheExactReceiptArtifactSet(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	temporary := t.TempDir()
	version := "0.37.0"
	commit := strings.Repeat("a", 40)
	names := []string{
		"talos-desktop.exe",
		"talos-worker.exe",
		"talosctl.exe",
		"talos-release-probe.exe",
		"talos-agent-" + version + "-windows-amd64-setup.exe",
	}
	artifacts := make([]map[string]any, 0, len(names))
	for index, name := range names {
		content := []byte{byte(index + 1)}
		if err := os.WriteFile(filepath.Join(temporary, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		artifacts = append(artifacts, map[string]any{
			"name": name, "sha256": hex.EncodeToString(digest[:]), "size": len(content),
			"signature_status": "NotSigned", "signer_subject": nil,
		})
	}
	receipt := map[string]any{
		"schema": "talos.windows-package-receipt/1", "version": version, "architecture": "amd64",
		"source_commit": commit, "signature_required": false, "release_probe_schema": "talos.release-probe/1",
		"artifacts": artifacts,
	}
	receiptPath := filepath.Join(temporary, "talos-agent-"+version+"-windows-amd64-receipt.json")
	writeJSON(t, receiptPath, receipt)
	verifier := filepath.Join(root, "packaging", "windows", "verify-package.ps1")
	if output, err := runPackageVerifier(verifier, receiptPath, commit); err != nil {
		t.Fatalf("exact unsigned package receipt was rejected: %v\n%s", err, output)
	}

	artifacts[len(artifacts)-1]["name"] = "talos-release-probe.exe"
	writeJSON(t, receiptPath, receipt)
	if output, err := runPackageVerifier(verifier, receiptPath, commit); err == nil {
		t.Fatalf("duplicate package artifact unexpectedly passed:\n%s", output)
	}

	artifacts[len(artifacts)-1]["name"] = names[len(names)-1]
	receipt["local_path"] = temporary
	writeJSON(t, receiptPath, receipt)
	if output, err := runPackageVerifier(verifier, receiptPath, commit); err == nil {
		t.Fatalf("unknown receipt field unexpectedly passed:\n%s", output)
	}
}

func TestWindowsPackagingVersionIsSynchronized(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	config := readFile(t, filepath.Join(root, "packaging", "windows", "config.yml"))
	project := readFile(t, filepath.Join(root, "packaging", "windows", "project.nsi"))
	taskfile := readFile(t, filepath.Join(root, "Taskfile.yml"))
	packageJSON := readFile(t, filepath.Join(root, "frontend", "package.json"))
	versionSource := readFile(t, filepath.Join(root, "internal", "version", "version.go"))
	worker := readFile(t, filepath.Join(root, "internal", "workeripc", "server.go"))
	health := readFile(t, filepath.Join(root, "internal", "transport", "wailsapi", "health.go"))
	signingWorkflow := readFile(t, filepath.Join(root, ".github", "workflows", "windows-signing.yml"))
	packagingReadme := readFile(t, filepath.Join(root, "packaging", "windows", "README.md"))
	match := regexp.MustCompile(`Application\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"`).FindStringSubmatch(versionSource)
	if len(match) != 2 {
		t.Fatal("internal/version/version.go does not contain one semantic application version")
	}
	releaseVersion := match[1]
	for path, content := range map[string]string{
		"config.yml":          config,
		"project.nsi":         project,
		"Taskfile.yml":        taskfile,
		"package.json":        packageJSON,
		"windows-signing.yml": signingWorkflow,
		"packaging/README.md": packagingReadme,
	} {
		if !strings.Contains(content, releaseVersion) {
			t.Errorf("%s does not contain release version %s", path, releaseVersion)
		}
	}
	for path, content := range map[string]string{"worker.go": worker, "health.go": health} {
		if !strings.Contains(content, "version.Application") {
			t.Errorf("%s does not use the central application version", path)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root was not found")
		}
		directory = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runPackageVerifier(verifier, receipt, commit string) ([]byte, error) {
	return exec.Command(
		"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", verifier,
		"-ReceiptPath", receipt, "-ExpectedSourceCommit", commit,
	).CombinedOutput()
}
