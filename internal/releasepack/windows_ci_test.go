//go:build windows

package releasepack

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var actionReferencePattern = regexp.MustCompile(`uses:\s+[^@\s]+@([^\s]+)`)
var fullCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func TestWindowsWorkflowsPinExternalActions(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	workflows := []string{
		"windows-ci.yml",
		"windows-signing.yml",
		"windows-upgrade-smoke.yml",
	}
	for _, name := range workflows {
		content := readFile(t, filepath.Join(root, ".github", "workflows", name))
		references := actionReferencePattern.FindAllStringSubmatch(content, -1)
		if len(references) == 0 {
			t.Errorf("%s contains no external action references", name)
		}
		for _, reference := range references {
			if !fullCommitPattern.MatchString(reference[1]) {
				t.Errorf("%s uses an action without a full commit pin: %s", name, reference[1])
			}
		}
	}
}

func TestWindowsCIHasNoSigningAuthority(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "windows-ci.yml"))
	for _, required := range []string{
		"runs-on: windows-2025",
		"permissions:\n  contents: read",
		"bun install --frozen-lockfile --ignore-scripts",
		"bun test tools/windows-upgrade-contract.test.ts tools/windows-upgrade-runs.test.ts tools/windows-upgrade-smoke.test.ts",
		"go test ./...",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("windows-ci.yml is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"self-hosted",
		"TALOS_SIGN_CERTIFICATE_SHA1",
		"pull_request_target",
		"id-token: write",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("windows-ci.yml contains forbidden authority %q", forbidden)
		}
	}
}

func TestWindowsSigningWorkflowIsManualAndIsolated(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "windows-signing.yml"))
	for _, required := range []string{
		"workflow_dispatch:",
		"if: github.ref == 'refs/heads/main'",
		"- talos-signing",
		"environment: windows-signing",
		"persist-credentials: false",
		"verify-signing-host.ps1",
		"-RequireSignature",
		"verify-package.ps1",
		"cancel-in-progress: false",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("windows-signing.yml is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request:",
		"pull_request_target",
		"id-token: write",
		"PFX",
		"PASSWORD",
		"continue-on-error",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("windows-signing.yml contains forbidden behavior %q", forbidden)
		}
	}
}

func TestWindowsUpgradeRunsWithoutSigningKey(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "windows-upgrade-smoke.yml"))
	for _, required := range []string{
		"workflow_dispatch:",
		"if: github.ref == 'refs/heads/main'",
		"- talos-upgrade-smoke",
		"actions: read",
		"TALOS_EXPECTED_SIGNER_SHA1",
		"TALOS_UPGRADE_SMOKE_EPHEMERAL",
		"oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6",
		"run-id: ${{ inputs.old_run_id }}",
		"run-id: ${{ inputs.new_run_id }}",
		"tools/windows-upgrade-runs.ts",
		"tools/windows-upgrade-smoke.ts",
		"talos-windows-upgrade-evidence",
		"if-no-files-found: error",
		"retention-days: 90",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("windows-upgrade-smoke.yml is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"talos-signing",
		"TALOS_SIGN_CERTIFICATE_SHA1",
		"pull_request_target",
		"continue-on-error",
		"upgrade-smoke.ps1",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("windows-upgrade-smoke.yml contains forbidden behavior %q", forbidden)
		}
	}
}

func TestWindowsSigningHostContract(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	content := readFile(t, filepath.Join(root, "packaging", "windows", "signing-host.json"))
	var contract struct {
		Schema                     string   `json:"schema"`
		Architecture               string   `json:"architecture"`
		GoMajorMinor               string   `json:"go_major_minor"`
		BunVersion                 string   `json:"bun_version"`
		NSISMajor                  int      `json:"nsis_major"`
		CertificateMinValidityDays int      `json:"certificate_min_validity_days"`
		RunnerLabels               []string `json:"runner_labels"`
	}
	if err := json.Unmarshal([]byte(content), &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "talos.windows-signing-host/1" || contract.Architecture != "amd64" {
		t.Fatalf("unexpected signing-host identity: %+v", contract)
	}
	if contract.GoMajorMinor != "1.26" || contract.BunVersion != "1.3.14" || contract.NSISMajor != 3 {
		t.Fatalf("unexpected signing-host toolchain: %+v", contract)
	}
	if contract.CertificateMinValidityDays < 30 {
		t.Fatalf("certificate safety window is too small: %d", contract.CertificateMinValidityDays)
	}
	if strings.Join(contract.RunnerLabels, ",") != "self-hosted,windows,x64,talos-signing" {
		t.Fatalf("unexpected signing runner labels: %v", contract.RunnerLabels)
	}

	preflight := readFile(t, filepath.Join(root, "packaging", "windows", "verify-signing-host.ps1"))
	for _, required := range []string{
		`Cert:\CurrentUser\My`,
		"HasPrivateKey",
		"1.3.6.1.5.5.7.3.3",
		"certificate_min_validity_days",
		"timestampUri.Scheme -ne 'https'",
		"status --porcelain=v1 --untracked-files=normal",
	} {
		if !strings.Contains(preflight, required) {
			t.Errorf("verify-signing-host.ps1 is missing %q", required)
		}
	}
}

func TestSignedArtifactVerificationPinsCommitAndPublisher(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	verifier := readFile(t, filepath.Join(root, "packaging", "windows", "verify-package.ps1"))
	for _, required := range []string{
		"ExpectedSourceCommit",
		"ExpectedSignerThumbprint",
		"receipt.source_commit",
		"SignerCertificate.Thumbprint",
	} {
		if !strings.Contains(verifier, required) {
			t.Errorf("verify-package.ps1 is missing %q", required)
		}
	}

	upgrade := readFile(t, filepath.Join(root, "tools", "windows-upgrade-smoke.ts"))
	for _, required := range []string{
		"TALOS_UPGRADE_SMOKE_EPHEMERAL",
		"verify-package.ps1",
		"ExpectedSignerThumbprint",
		"verifyInstalledBinaries",
		"runProbe",
		"RELEASE_PROBE_FAILED",
		"direct_rollback_read_verified",
		"upgradeEvidenceSchema",
	} {
		if !strings.Contains(upgrade, required) {
			t.Errorf("windows-upgrade-smoke.ts is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"preserve-me.txt",
		"vault-data-must-survive-upgrade",
		"Remove-Item",
		"TALOS_SIGN_CERTIFICATE_SHA1",
	} {
		if strings.Contains(upgrade, forbidden) {
			t.Errorf("windows-upgrade-smoke.ts contains forbidden behavior %q", forbidden)
		}
	}

	runResolver := readFile(t, filepath.Join(root, "tools", "windows-upgrade-runs.ts"))
	for _, required := range []string{
		".github/workflows/windows-signing.yml",
		"workflow_dispatch",
		"head_branch",
		"conclusion",
		"old-to-new",
		"new-to-verifier",
	} {
		if !strings.Contains(runResolver, required) {
			t.Errorf("windows-upgrade-runs.ts is missing %q", required)
		}
	}
}

func TestWindowsUpgradeEvidenceContractIsStrictAndPathFree(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	schemaContent := readFile(t, filepath.Join(root, "contracts", "jsonschema", "release", "v1", "windows-upgrade-evidence.schema.json"))
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaContent), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatal("Windows upgrade evidence schema must use draft 2020-12 and reject unknown top-level fields")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Windows upgrade evidence schema properties are missing")
	}
	for _, forbidden := range []string{"local_path", "install_root", "package_root", "runner_temp"} {
		if _, found := properties[forbidden]; found {
			t.Errorf("Windows upgrade evidence schema exposes local path field %q", forbidden)
		}
	}

	validContent := readFile(t, filepath.Join(root, "contracts", "fixtures", "release", "v1", "valid-windows-upgrade-evidence.json"))
	var valid map[string]any
	if err := json.Unmarshal([]byte(validContent), &valid); err != nil {
		t.Fatal(err)
	}
	if valid["schema"] != "talos.windows-upgrade-evidence/1" || valid["status"] != "passed" {
		t.Fatalf("unexpected valid Windows upgrade evidence fixture: %+v", valid)
	}
	for _, forbidden := range []string{"local_path", "install_root", "package_root", "runner_temp"} {
		if strings.Contains(validContent, forbidden) {
			t.Errorf("valid Windows upgrade evidence fixture contains forbidden local field %q", forbidden)
		}
	}

	invalidContent := readFile(t, filepath.Join(root, "contracts", "fixtures", "release", "v1", "invalid-windows-upgrade-evidence-local-path.json"))
	if !strings.Contains(invalidContent, `"local_path"`) {
		t.Fatal("invalid Windows upgrade evidence fixture does not exercise local-path rejection")
	}
}
