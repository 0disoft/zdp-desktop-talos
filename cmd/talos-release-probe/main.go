package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/bootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/version"
)

const reportSchema = "talos.release-probe/1"

type report struct {
	Schema                 string `json:"schema"`
	Action                 string `json:"action"`
	ApplicationVersion     string `json:"application_version"`
	VaultID                string `json:"vault_id"`
	Revision               int    `json:"revision"`
	RetentionDays          int    `json:"retention_days"`
	BackupID               string `json:"backup_id,omitempty"`
	BackupCiphertextSHA256 string `json:"backup_ciphertext_sha256,omitempty"`
	BackupSourceSchema     int    `json:"backup_source_schema,omitempty"`
	BackupTargetSchema     int    `json:"backup_target_schema,omitempty"`
	RestoreState           string `json:"restore_state,omitempty"`
}

type commandPlan struct {
	action            string
	vaultID           string
	expectedRevision  int
	expectedRetention int
	retentionDays     int
	backupPath        string
	backupID          string
	backupSHA256      string
	confirmation      string
}

func main() {
	result, err := run(context.Background(), os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release probe: %v\n", err)
		os.Exit(10)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "release probe: encode result: %v\n", err)
		os.Exit(11)
	}
}

func run(parent context.Context, arguments []string) (report, error) {
	if parent == nil || len(arguments) == 0 {
		return report{}, usageError()
	}
	plan, err := parseCommand(arguments)
	if err != nil {
		return report{}, err
	}
	root, err := authorizedRoot()
	if err != nil {
		return report{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	creator, err := bootstrap.NewVaultCreator(root)
	if err != nil {
		return report{}, fmt.Errorf("open release-probe Vault runtime: %w", err)
	}
	return plan.execute(ctx, creator)
}

func parseCommand(arguments []string) (commandPlan, error) {
	if len(arguments) == 0 {
		return commandPlan{}, usageError()
	}
	switch arguments[0] {
	case "create":
		flags := newFlags("create")
		retentionDays := flags.Int("retention-days", 0, "retention days for the release probe Vault")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *retentionDays < 1 {
			return commandPlan{}, usageError()
		}
		return commandPlan{action: "create", retentionDays: *retentionDays}, nil
	case "inspect":
		flags := newFlags("inspect")
		vaultID := flags.String("vault-id", "", "Vault ID")
		expectedRevision := flags.Int("expected-revision", 0, "expected Vault revision")
		expectedRetention := flags.Int("expected-retention-days", 0, "expected retention days")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *vaultID == "" || *expectedRevision < 1 || *expectedRetention < 1 {
			return commandPlan{}, usageError()
		}
		return commandPlan{action: "inspect", vaultID: *vaultID, expectedRevision: *expectedRevision, expectedRetention: *expectedRetention}, nil
	case "update-retention":
		flags := newFlags("update-retention")
		vaultID := flags.String("vault-id", "", "Vault ID")
		expectedRevision := flags.Int("expected-revision", 0, "expected Vault revision")
		retentionDays := flags.Int("retention-days", 0, "new retention days")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *vaultID == "" || *expectedRevision < 1 || *retentionDays < 1 {
			return commandPlan{}, usageError()
		}
		return commandPlan{action: "update-retention", vaultID: *vaultID, expectedRevision: *expectedRevision, retentionDays: *retentionDays}, nil
	case "backup":
		flags := newFlags("backup")
		vaultID := flags.String("vault-id", "", "Vault ID")
		expectedRevision := flags.Int("expected-revision", 0, "expected Vault revision")
		destination := flags.String("destination", "", "backup destination beneath RUNNER_TEMP")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *vaultID == "" || *expectedRevision < 1 {
			return commandPlan{}, usageError()
		}
		backupPath, err := authorizedTemporaryPath(*destination)
		if err != nil {
			return commandPlan{}, err
		}
		return commandPlan{action: "backup", vaultID: *vaultID, expectedRevision: *expectedRevision, backupPath: backupPath}, nil
	case "restore":
		flags := newFlags("restore")
		vaultID := flags.String("vault-id", "", "Vault ID")
		expectedRevision := flags.Int("expected-revision", 0, "expected live Vault revision")
		source := flags.String("source", "", "backup source beneath RUNNER_TEMP")
		backupID := flags.String("backup-id", "", "expected backup ID")
		backupSHA256 := flags.String("backup-sha256", "", "expected backup ciphertext SHA-256")
		confirmation := flags.String("confirmation", "", "exact Vault ID confirmation")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *vaultID == "" || *expectedRevision < 1 || *backupID == "" || *backupSHA256 == "" || *confirmation == "" {
			return commandPlan{}, usageError()
		}
		backupPath, err := authorizedTemporaryPath(*source)
		if err != nil {
			return commandPlan{}, err
		}
		return commandPlan{action: "restore", vaultID: *vaultID, expectedRevision: *expectedRevision, backupPath: backupPath, backupID: *backupID, backupSHA256: *backupSHA256, confirmation: *confirmation}, nil
	case "purge":
		flags := newFlags("purge")
		vaultID := flags.String("vault-id", "", "Vault ID")
		expectedRevision := flags.Int("expected-revision", 0, "expected Vault revision")
		confirmation := flags.String("confirmation", "", "exact Vault ID confirmation")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 || *vaultID == "" || *expectedRevision < 1 || *confirmation == "" {
			return commandPlan{}, usageError()
		}
		return commandPlan{action: "purge", vaultID: *vaultID, expectedRevision: *expectedRevision, confirmation: *confirmation}, nil
	default:
		return commandPlan{}, usageError()
	}
}

func (plan commandPlan) execute(ctx context.Context, creator *vaultbootstrap.Creator) (report, error) {
	switch plan.action {
	case "create":
		return createVault(ctx, creator, plan)
	case "inspect":
		return inspectVault(ctx, creator, plan)
	case "update-retention":
		return updateRetention(ctx, creator, plan)
	case "backup":
		return backupVault(ctx, creator, plan)
	case "restore":
		return restoreVault(ctx, creator, plan)
	case "purge":
		return purgeVault(ctx, creator, plan)
	default:
		return report{}, usageError()
	}
}

func createVault(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Create(ctx, vaultbootstrap.CreateInput{RetentionDays: plan.retentionDays})
	if err != nil {
		return report{}, fmt.Errorf("create probe Vault: %w", err)
	}
	defer session.Close()
	return vaultReport("create", session), nil
}

func inspectVault(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Open(ctx, plan.vaultID)
	if err != nil {
		return report{}, fmt.Errorf("open probe Vault: %w", err)
	}
	defer session.Close()
	if session.Record.Revision != plan.expectedRevision || session.Record.RetentionDays != plan.expectedRetention {
		return report{}, fmt.Errorf("probe Vault state mismatch: revision=%d retention_days=%d", session.Record.Revision, session.Record.RetentionDays)
	}
	return vaultReport("inspect", session), nil
}

func updateRetention(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Open(ctx, plan.vaultID)
	if err != nil {
		return report{}, fmt.Errorf("open probe Vault for mutation: %w", err)
	}
	defer session.Close()
	_, err = session.UpdateRetention(ctx, vaultbootstrap.UpdateRetentionInput{
		ExpectedRevision: plan.expectedRevision,
		RetentionDays:    plan.retentionDays,
		IdempotencyKey:   "release-probe-retention:" + strconv.Itoa(plan.expectedRevision) + ":" + strconv.Itoa(plan.retentionDays),
	})
	if err != nil {
		return report{}, fmt.Errorf("update probe Vault retention: %w", err)
	}
	return vaultReport("update-retention", session), nil
}

func backupVault(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Open(ctx, plan.vaultID)
	if err != nil {
		return report{}, fmt.Errorf("open probe Vault for backup: %w", err)
	}
	defer session.Close()
	if session.Record.Revision != plan.expectedRevision {
		return report{}, fmt.Errorf("probe Vault revision changed before backup: %d", session.Record.Revision)
	}
	receipt, err := session.CreateBackup(ctx, plan.backupPath, version.Application)
	if err != nil {
		return report{}, fmt.Errorf("create probe Vault backup: %w", err)
	}
	preflight, err := session.PreflightBackup(ctx, receipt.Path, version.Application)
	if err != nil {
		return report{}, fmt.Errorf("preflight probe Vault backup: %w", err)
	}
	if receipt.BackupID != preflight.BackupID || receipt.CiphertextSHA256 != preflight.CiphertextSHA256 || receipt.VaultID != preflight.VaultID {
		return report{}, errors.New("probe Vault backup receipt and preflight differ")
	}
	result := vaultReport("backup", session)
	result.BackupID = preflight.BackupID
	result.BackupCiphertextSHA256 = preflight.CiphertextSHA256
	result.BackupSourceSchema = preflight.SourceSchemaVersion
	result.BackupTargetSchema = preflight.TargetSchemaVersion
	return result, nil
}

func restoreVault(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Open(ctx, plan.vaultID)
	if err != nil {
		return report{}, fmt.Errorf("open probe Vault for restore: %w", err)
	}
	restored, err := creator.RestoreBackup(ctx, session, vaultbootstrap.RestoreBackupInput{
		Source: plan.backupPath, ExpectedBackupID: plan.backupID, ExpectedCiphertextSHA256: plan.backupSHA256,
		ExpectedRevision: plan.expectedRevision, Confirmation: plan.confirmation, ApplicationVersion: version.Application,
	})
	if err != nil {
		if restored.Session != nil {
			_ = restored.Session.Close()
		}
		return report{}, fmt.Errorf("restore probe Vault: %w", err)
	}
	defer restored.Session.Close()
	result := vaultReport("restore", restored.Session)
	result.BackupID = restored.Restore.BackupID
	result.BackupCiphertextSHA256 = restored.Restore.CiphertextSHA256
	result.BackupSourceSchema = restored.Restore.SourceSchemaVersion
	result.BackupTargetSchema = restored.Restore.TargetSchemaVersion
	result.RestoreState = string(restored.Restore.State)
	return result, nil
}

func purgeVault(ctx context.Context, creator *vaultbootstrap.Creator, plan commandPlan) (report, error) {
	session, err := creator.Open(ctx, plan.vaultID)
	if err != nil {
		return report{}, fmt.Errorf("open probe Vault for purge: %w", err)
	}
	if err := creator.HardPurge(ctx, session, vaultbootstrap.HardPurgeInput{ExpectedRevision: plan.expectedRevision, Confirmation: plan.confirmation}); err != nil {
		return report{}, fmt.Errorf("purge probe Vault: %w", err)
	}
	entries, err := creator.List(ctx)
	if err != nil {
		return report{}, fmt.Errorf("verify probe Vault purge: %w", err)
	}
	for _, entry := range entries {
		if entry.VaultID == plan.vaultID {
			return report{}, errors.New("purged probe Vault remains in protected catalog")
		}
	}
	return report{Schema: reportSchema, Action: "purge", ApplicationVersion: version.Application, VaultID: plan.vaultID, Revision: plan.expectedRevision}, nil
}

func authorizedRoot() (string, error) {
	if os.Getenv("CI") != "true" || os.Getenv("TALOS_RELEASE_PROBE") != "1" || runtime.GOOS != "windows" {
		return "", errors.New("release probe requires an explicitly enabled Windows CI account")
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve current-user cache root: %w", err)
	}
	expected, err := filepath.Abs(filepath.Join(cacheRoot, "0disoft", "Talos Agent"))
	if err != nil {
		return "", fmt.Errorf("resolve release-probe data root: %w", err)
	}
	declared, err := filepath.Abs(os.Getenv("TALOS_RELEASE_PROBE_ROOT"))
	if err != nil || !strings.EqualFold(filepath.Clean(declared), filepath.Clean(expected)) {
		return "", errors.New("TALOS_RELEASE_PROBE_ROOT must equal the current user's Talos local-data root")
	}
	return filepath.Clean(expected), nil
}

func authorizedTemporaryPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(os.Getenv("RUNNER_TEMP")) == "" {
		return "", errors.New("release-probe backup path and RUNNER_TEMP are required")
	}
	root, err := filepath.Abs(os.Getenv("RUNNER_TEMP"))
	if err != nil {
		return "", fmt.Errorf("resolve RUNNER_TEMP: %w", err)
	}
	candidate, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve release-probe backup path: %w", err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("release-probe backup path must be a file beneath RUNNER_TEMP")
	}
	return filepath.Clean(candidate), nil
}

func vaultReport(action string, session *vaultbootstrap.Session) report {
	return report{Schema: reportSchema, Action: action, ApplicationVersion: version.Application, VaultID: session.Record.ID, Revision: session.Record.Revision, RetentionDays: session.Record.RetentionDays}
}

func newFlags(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	return flags
}

func usageError() error {
	return errors.New("usage: talos-release-probe <create|inspect|update-retention|backup|restore|purge> [flags]")
}
