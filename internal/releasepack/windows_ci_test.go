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
		"run-id: ${{ inputs.old_run_id }}",
		"run-id: ${{ inputs.new_run_id }}",
		"upgrade-smoke.ps1",
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

	upgrade := readFile(t, filepath.Join(root, "packaging", "windows", "upgrade-smoke.ps1"))
	for _, required := range []string{
		"TALOS_EXPECTED_SIGNER_SHA1",
		"SignerCertificate.Thumbprint",
		"unexpected publisher",
	} {
		if !strings.Contains(upgrade, required) {
			t.Errorf("upgrade-smoke.ps1 is missing %q", required)
		}
	}
}
