//go:build windows

package releasepack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPackagingPowerShellParses(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	scripts := []string{
		"package.ps1",
		"sign-artifact.ps1",
		"upgrade-smoke.ps1",
		"verify-package.ps1",
		"verify-signing-host.ps1",
	}
	for _, name := range scripts {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
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

func TestWindowsPackagingVersionIsSynchronized(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	config := readFile(t, filepath.Join(root, "packaging", "windows", "config.yml"))
	project := readFile(t, filepath.Join(root, "packaging", "windows", "project.nsi"))
	taskfile := readFile(t, filepath.Join(root, "Taskfile.yml"))
	packageJSON := readFile(t, filepath.Join(root, "frontend", "package.json"))
	worker := readFile(t, filepath.Join(root, "internal", "workeripc", "server.go"))
	health := readFile(t, filepath.Join(root, "internal", "transport", "wailsapi", "health.go"))
	signingWorkflow := readFile(t, filepath.Join(root, ".github", "workflows", "windows-signing.yml"))
	packagingReadme := readFile(t, filepath.Join(root, "packaging", "windows", "README.md"))
	for path, content := range map[string]string{
		"config.yml":          config,
		"project.nsi":         project,
		"Taskfile.yml":        taskfile,
		"package.json":        packageJSON,
		"worker.go":           worker,
		"health.go":           health,
		"windows-signing.yml": signingWorkflow,
		"packaging/README.md": packagingReadme,
	} {
		if !strings.Contains(content, "0.1.6") {
			t.Errorf("%s does not contain release version 0.1.6", path)
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
