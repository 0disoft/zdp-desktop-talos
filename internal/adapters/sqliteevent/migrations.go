package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const currentSchemaVersion = 20

var ErrUnsupportedSchema = errors.New("sqlite event store schema is newer than this application")

type migration struct {
	version    int
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS events (
				event_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL,
				event_type TEXT NOT NULL,
				schema_version INTEGER NOT NULL CHECK (schema_version > 0),
				sensitivity TEXT NOT NULL CHECK (sensitivity IN ('public','private','sensitive','secret')),
				payload_envelope BLOB NOT NULL,
				occurred_at TEXT NOT NULL
			) STRICT`,
			`CREATE TABLE IF NOT EXISTS idempotency_keys (
				idempotency_key TEXT PRIMARY KEY,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
		},
	},
	{
		version: 2,
		statements: []string{
			`ALTER TABLE idempotency_keys ADD COLUMN request_hash BLOB`,
		},
	},
	{
		version: 3,
		statements: []string{
			`CREATE TABLE vault_states (
				vault_id TEXT PRIMARY KEY,
				revision INTEGER NOT NULL CHECK (revision > 0),
				status TEXT NOT NULL CHECK (status IN ('active','purged')),
				retention_days INTEGER NOT NULL CHECK (retention_days BETWEEN 1 AND 3650),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
		},
	},
	{
		version: 4,
		statements: []string{
			`CREATE TABLE artifacts (
				artifact_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL,
				schema_version INTEGER NOT NULL CHECK (schema_version > 0),
				sensitivity TEXT NOT NULL CHECK (sensitivity IN ('public','private','sensitive')),
				content_type TEXT NOT NULL,
				size_bytes INTEGER NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 67108864),
				content_hash TEXT NOT NULL,
				ciphertext_hash TEXT NOT NULL,
				storage_name TEXT NOT NULL,
				staging_name TEXT NOT NULL,
				state TEXT NOT NULL CHECK (state IN ('staged','ready')),
				created_at TEXT NOT NULL
			) STRICT`,
			`CREATE INDEX artifacts_state_idx ON artifacts(state, created_at)`,
		},
	},
	{
		version: 5,
		statements: []string{
			`CREATE TABLE tasks (
				task_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				workspace_root_hash TEXT NOT NULL,
				baseline_commit TEXT NOT NULL,
				status TEXT NOT NULL CHECK (status IN ('contracted')),
				current_revision INTEGER NOT NULL CHECK (current_revision > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				UNIQUE(task_id, baseline_commit)
			) STRICT`,
			`CREATE TABLE task_contract_revisions (
				task_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision > 0),
				baseline_commit TEXT NOT NULL,
				created_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				PRIMARY KEY(task_id, revision),
				FOREIGN KEY(task_id, baseline_commit) REFERENCES tasks(task_id, baseline_commit) ON DELETE RESTRICT
			) STRICT`,
			`CREATE INDEX tasks_vault_created_idx ON tasks(vault_id, created_at, task_id)`,
		},
	},
	{
		version: 6,
		statements: []string{
			`CREATE TABLE decisions (
				decision_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				question_revision INTEGER NOT NULL CHECK (question_revision > 0),
				category TEXT NOT NULL CHECK (category IN ('blocking','quality','follow_up')),
				state TEXT NOT NULL CHECK (state IN ('open','answered','conflicted')),
				expected_repository_revision TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				question_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE INDEX decisions_task_state_idx ON decisions(task_id, state, created_at, decision_id)`,
			`CREATE TABLE decision_answers (
				answer_id TEXT PRIMARY KEY,
				decision_id TEXT NOT NULL REFERENCES decisions(decision_id) ON DELETE RESTRICT,
				question_revision INTEGER NOT NULL CHECK (question_revision > 0),
				expected_repository_revision TEXT NOT NULL,
				answer_hash TEXT NOT NULL,
				created_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				UNIQUE(decision_id, answer_hash)
			) STRICT`,
		},
	},
	{
		version: 7,
		statements: []string{
			`CREATE TABLE decision_answers_v7 (
				answer_id TEXT PRIMARY KEY,
				decision_id TEXT NOT NULL REFERENCES decisions(decision_id) ON DELETE RESTRICT,
				question_revision INTEGER NOT NULL CHECK (question_revision > 0),
				expected_repository_revision TEXT NOT NULL,
				answer_hash TEXT NOT NULL,
				created_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				UNIQUE(decision_id, question_revision, answer_hash)
			) STRICT`,
			`INSERT INTO decision_answers_v7(answer_id, decision_id, question_revision, expected_repository_revision, answer_hash, created_at, event_id)
			 SELECT answer_id, decision_id, question_revision, expected_repository_revision, answer_hash, created_at, event_id FROM decision_answers`,
			`DROP TABLE decision_answers`,
			`ALTER TABLE decision_answers_v7 RENAME TO decision_answers`,
		},
	},
	{
		version: 8,
		statements: []string{
			`CREATE TABLE permission_grants (
				grant_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				outcome TEXT NOT NULL CHECK (outcome IN ('deny','allow_once','allow_task','allow_workspace')),
				state TEXT NOT NULL CHECK (state IN ('active','consumed','revoked')),
				capability_hash TEXT NOT NULL,
				task_id TEXT REFERENCES tasks(task_id) ON DELETE RESTRICT,
				workspace_hash TEXT NOT NULL,
				created_at TEXT NOT NULL,
				expires_at TEXT,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE INDEX permission_grants_scope_idx ON permission_grants(vault_id, workspace_hash, state, task_id, created_at, grant_id)`,
			`CREATE TABLE runs (
				run_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				workspace_hash TEXT NOT NULL,
				state TEXT NOT NULL CHECK (state IN ('active','completed','failed','canceled','unknown')),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE UNIQUE INDEX runs_one_active_workspace_idx ON runs(vault_id, workspace_hash) WHERE state = 'active'`,
			`CREATE TABLE attempts (
				attempt_id TEXT PRIMARY KEY,
				run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE RESTRICT,
				call_id TEXT NOT NULL UNIQUE,
				capability_hash TEXT NOT NULL,
				grant_id TEXT REFERENCES permission_grants(grant_id) ON DELETE RESTRICT,
				state TEXT NOT NULL CHECK (state IN ('dispatch_pending','succeeded','failed','canceled','unknown')),
				exit_code INTEGER,
				safe_error_code TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				prepared_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE INDEX attempts_run_state_idx ON attempts(run_id, state, created_at, attempt_id)`,
			`CREATE UNIQUE INDEX attempts_one_pending_run_idx ON attempts(run_id) WHERE state = 'dispatch_pending'`,
		},
	},
	{
		version: 9,
		statements: []string{
			`CREATE TABLE permission_requests (
				request_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				workspace_hash TEXT NOT NULL,
				capability_hash TEXT NOT NULL,
				state TEXT NOT NULL CHECK (state IN ('open','approved','denied')),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE UNIQUE INDEX permission_requests_one_open_intent_idx ON permission_requests(vault_id, task_id, capability_hash) WHERE state = 'open'`,
			`CREATE INDEX permission_requests_open_task_idx ON permission_requests(vault_id, task_id, state, created_at, request_id)`,
		},
	},
	{
		version: 10,
		statements: []string{
			`CREATE TABLE verification_evidence (
				evidence_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE RESTRICT,
				attempt_id TEXT NOT NULL UNIQUE REFERENCES attempts(attempt_id) ON DELETE RESTRICT,
				contract_revision INTEGER NOT NULL CHECK (contract_revision > 0),
				command_index INTEGER NOT NULL CHECK (command_index >= 0),
				baseline_commit TEXT NOT NULL,
				worktree_state_hash TEXT NOT NULL,
				capability_hash TEXT NOT NULL,
				exit_code INTEGER NOT NULL CHECK (exit_code = 0),
				started_at TEXT NOT NULL,
				finished_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE INDEX verification_evidence_task_idx ON verification_evidence(vault_id, task_id, finished_at, evidence_id)`,
		},
	},
	{
		version: 11,
		statements: []string{
			`CREATE TABLE patch_actions (
				action_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				kind TEXT NOT NULL CHECK (kind IN ('apply','discard')),
				state TEXT NOT NULL CHECK (state IN ('pending','succeeded','failed','unknown')),
				contract_revision INTEGER NOT NULL CHECK (contract_revision > 0),
				patch_hash TEXT NOT NULL,
				worktree_state_hash TEXT NOT NULL,
				evidence_id TEXT REFERENCES verification_evidence(evidence_id) ON DELETE RESTRICT,
				safe_error_code TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE UNIQUE INDEX patch_actions_one_unresolved_task_idx ON patch_actions(task_id) WHERE state IN ('pending','unknown')`,
			`CREATE TABLE task_outcomes (
				task_id TEXT PRIMARY KEY REFERENCES tasks(task_id) ON DELETE RESTRICT,
				status TEXT NOT NULL CHECK (status IN ('completed','discarded')),
				action_id TEXT NOT NULL UNIQUE REFERENCES patch_actions(action_id) ON DELETE RESTRICT,
				completed_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
		},
	},
	{
		version: 12,
		statements: []string{
			`CREATE TABLE account_links (
				vault_id TEXT PRIMARY KEY REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				membership_id TEXT NOT NULL UNIQUE,
				state TEXT NOT NULL CHECK (state IN ('linked','unlinked')),
				revision INTEGER NOT NULL CHECK (revision > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT
			) STRICT`,
		},
	},
	{
		version: 13,
		statements: []string{
			`CREATE TABLE model_egress_receipts (
				receipt_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE RESTRICT,
				contract_revision INTEGER NOT NULL CHECK (contract_revision > 0),
				provider_key TEXT NOT NULL,
				model_key TEXT NOT NULL,
				request_id TEXT NOT NULL,
				prompt_version TEXT NOT NULL,
				context_hash TEXT NOT NULL,
				request_hash TEXT NOT NULL,
				response_hash TEXT NOT NULL DEFAULT '',
				provider_call_id TEXT NOT NULL DEFAULT '',
				context_items INTEGER NOT NULL CHECK (context_items BETWEEN 1 AND 64),
				input_bytes INTEGER NOT NULL CHECK (input_bytes BETWEEN 1 AND 1048576),
				output_bytes INTEGER NOT NULL DEFAULT 0 CHECK (output_bytes BETWEEN 0 AND 1048576),
				redaction_count INTEGER NOT NULL CHECK (redaction_count BETWEEN 0 AND 10000),
				input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
				cached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0 AND cached_input_tokens <= input_tokens),
				output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
				status TEXT NOT NULL CHECK (status IN ('prepared','completed','failed')),
				safe_error_code TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, request_id)
			) STRICT`,
			`CREATE INDEX model_egress_task_idx ON model_egress_receipts(vault_id, task_id, created_at, receipt_id)`,
		},
	},
	{
		version: 14,
		statements: []string{
			`CREATE TABLE memory_records (
				memory_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				kind TEXT NOT NULL CHECK (kind IN ('preference','constraint','decision','procedure','failure_pattern','environment_fact')),
				state TEXT NOT NULL CHECK (state IN ('candidate','approved','stable','stale','rejected','quarantined','superseded','deprecated')),
				scope_kind TEXT NOT NULL CHECK (scope_kind IN ('vault','workspace')),
				workspace_root_hash TEXT NOT NULL,
				sensitivity TEXT NOT NULL CHECK (sensitivity IN ('public','private','sensitive')),
				confidence INTEGER NOT NULL CHECK (confidence BETWEEN 0 AND 100),
				revision INTEGER NOT NULL CHECK (revision > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				reviewed_at TEXT NOT NULL DEFAULT '',
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				CHECK ((scope_kind = 'vault' AND workspace_root_hash = '') OR (scope_kind = 'workspace' AND length(workspace_root_hash) = 64)),
				CHECK ((state = 'candidate' AND reviewed_at = '') OR (state <> 'candidate' AND reviewed_at <> ''))
			) STRICT`,
			`CREATE INDEX memory_records_candidate_idx ON memory_records(vault_id, state, created_at, memory_id)`,
			`CREATE INDEX memory_records_context_idx ON memory_records(vault_id, state, scope_kind, workspace_root_hash, confidence, updated_at, memory_id)`,
		},
	},
	{
		version: 15,
		statements: []string{
			`ALTER TABLE memory_records ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE memory_records ADD COLUMN superseded_by_memory_id TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX memory_records_expiry_idx ON memory_records(vault_id, state, expires_at, memory_id)`,
		},
	},
	{
		version: 16,
		statements: []string{
			`CREATE TABLE sync_devices (
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				device_id TEXT NOT NULL,
				public_key BLOB NOT NULL CHECK (length(public_key) = 32),
				state TEXT NOT NULL CHECK (state IN ('active','revoked')),
				revision INTEGER NOT NULL CHECK (revision > 0),
				next_sequence INTEGER NOT NULL CHECK (next_sequence > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				PRIMARY KEY(vault_id, device_id)
			) STRICT`,
			`CREATE TABLE sync_pack_receipts (
				pack_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				sequence_start INTEGER NOT NULL CHECK (sequence_start > 0),
				sequence_end INTEGER NOT NULL CHECK (sequence_end >= sequence_start),
				event_count INTEGER NOT NULL CHECK (event_count BETWEEN 1 AND 512 AND event_count = sequence_end - sequence_start + 1),
				ciphertext_hash TEXT NOT NULL CHECK (length(ciphertext_hash) = 64),
				pack_envelope BLOB NOT NULL CHECK (length(pack_envelope) > 0),
				state TEXT NOT NULL CHECK (state IN ('validated')),
				received_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, device_id, sequence_start)
			) STRICT`,
			`CREATE INDEX sync_pack_receipts_device_idx ON sync_pack_receipts(vault_id, device_id, sequence_start, pack_id)`,
		},
	},
	{
		version: 17,
		statements: []string{
			`CREATE TABLE sync_event_origins (
				event_id TEXT PRIMARY KEY REFERENCES events(event_id) ON DELETE RESTRICT,
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				device_seq INTEGER NOT NULL CHECK (device_seq > 0),
				origin_kind TEXT NOT NULL CHECK (origin_kind IN ('local','imported')),
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, device_id, device_seq)
			) STRICT`,
			`CREATE TABLE sync_export_heads (
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				next_sequence INTEGER NOT NULL CHECK (next_sequence > 0),
				updated_at TEXT NOT NULL,
				PRIMARY KEY(vault_id, device_id),
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT
			) STRICT`,
			`CREATE TABLE sync_export_batches (
				export_id TEXT PRIMARY KEY,
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				sequence_start INTEGER NOT NULL CHECK (sequence_start > 0),
				sequence_end INTEGER NOT NULL CHECK (sequence_end >= sequence_start),
				event_count INTEGER NOT NULL CHECK (event_count BETWEEN 1 AND 512 AND event_count = sequence_end - sequence_start + 1),
				state TEXT NOT NULL CHECK (state IN ('preparing','ready')),
				pack_id TEXT NOT NULL DEFAULT '',
				ciphertext_hash TEXT NOT NULL DEFAULT '',
				pack_envelope BLOB NOT NULL DEFAULT X'',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, device_id, sequence_start),
				CHECK ((state = 'preparing' AND pack_id = '' AND ciphertext_hash = '' AND length(pack_envelope) = 0) OR (state = 'ready' AND length(pack_id) = 71 AND length(ciphertext_hash) = 64 AND length(pack_envelope) > 0))
			) STRICT`,
			`CREATE UNIQUE INDEX sync_export_batches_one_preparing_idx ON sync_export_batches(vault_id, device_id) WHERE state = 'preparing'`,
			`CREATE INDEX sync_export_batches_ready_idx ON sync_export_batches(vault_id, device_id, state, sequence_start, export_id)`,
			`CREATE TABLE sync_export_batch_events (
				export_id TEXT NOT NULL REFERENCES sync_export_batches(export_id) ON DELETE RESTRICT,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				device_seq INTEGER NOT NULL CHECK (device_seq > 0),
				PRIMARY KEY(export_id, device_seq)
			) STRICT`,
		},
	},
	{
		version: 18,
		statements: []string{
			`CREATE TABLE sync_replay_items (
				pack_id TEXT NOT NULL REFERENCES sync_pack_receipts(pack_id) ON DELETE RESTRICT,
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				device_seq INTEGER NOT NULL CHECK (device_seq > 0),
				event_id TEXT NOT NULL CHECK (length(event_id) BETWEEN 1 AND 128),
				event_hash TEXT NOT NULL CHECK (length(event_hash) = 64),
				state TEXT NOT NULL CHECK (state IN ('applied','conflicted','quarantined')),
				reason_code TEXT NOT NULL CHECK (length(reason_code) BETWEEN 1 AND 64),
				recorded_at TEXT NOT NULL,
				PRIMARY KEY(pack_id, device_seq),
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, device_id, device_seq)
			) STRICT`,
			`CREATE INDEX sync_replay_items_event_idx ON sync_replay_items(vault_id, event_id, state)`,
			`CREATE TABLE sync_replay_batches (
				pack_id TEXT PRIMARY KEY REFERENCES sync_pack_receipts(pack_id) ON DELETE RESTRICT,
				vault_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				sequence_start INTEGER NOT NULL CHECK (sequence_start > 0),
				sequence_end INTEGER NOT NULL CHECK (sequence_end >= sequence_start),
				event_count INTEGER NOT NULL CHECK (event_count BETWEEN 1 AND 512 AND event_count = sequence_end - sequence_start + 1),
				applied_count INTEGER NOT NULL CHECK (applied_count >= 0),
				conflicted_count INTEGER NOT NULL CHECK (conflicted_count >= 0),
				quarantined_count INTEGER NOT NULL CHECK (quarantined_count >= 0),
				completed_at TEXT NOT NULL,
				event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				FOREIGN KEY(vault_id, device_id) REFERENCES sync_devices(vault_id, device_id) ON DELETE RESTRICT,
				CHECK (applied_count + conflicted_count + quarantined_count = event_count)
			) STRICT`,
			`CREATE INDEX sync_replay_batches_device_idx ON sync_replay_batches(vault_id, device_id, sequence_start, pack_id)`,
		},
	},
	{
		version: 19,
		statements: []string{
			`CREATE TABLE sync_enrollments (
				enrollment_id TEXT PRIMARY KEY CHECK (length(enrollment_id) BETWEEN 1 AND 96),
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				role TEXT NOT NULL CHECK (role IN ('issuer','recipient')),
				state TEXT NOT NULL CHECK (state IN ('offered','accepted','completed')),
				peer_device_id TEXT,
				offer_hash TEXT NOT NULL CHECK (length(offer_hash) = 64),
				acceptance_hash TEXT CHECK (acceptance_hash IS NULL OR length(acceptance_hash) = 64),
				acceptance_envelope BLOB,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				CHECK (
					(role = 'issuer' AND state = 'offered' AND peer_device_id IS NULL AND acceptance_hash IS NULL AND acceptance_envelope IS NULL)
					OR (role = 'issuer' AND state = 'completed' AND peer_device_id IS NOT NULL AND acceptance_hash IS NOT NULL AND acceptance_envelope IS NULL)
					OR (role = 'recipient' AND state = 'accepted' AND peer_device_id IS NOT NULL AND acceptance_hash IS NOT NULL AND acceptance_envelope IS NOT NULL)
				)
			) STRICT`,
			`CREATE INDEX sync_enrollments_vault_state_idx ON sync_enrollments(vault_id, state, expires_at, enrollment_id)`,
		},
	},
	{
		version: 20,
		statements: []string{
			`ALTER TABLE tasks ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`,
			`CREATE TABLE workspace_mappings (
				workspace_id TEXT PRIMARY KEY CHECK (length(workspace_id) = 77),
				vault_id TEXT NOT NULL REFERENCES vault_states(vault_id) ON DELETE RESTRICT,
				source_workspace_hash TEXT NOT NULL CHECK (length(source_workspace_hash) = 64),
				local_root_hash TEXT NOT NULL CHECK (length(local_root_hash) = 64),
				verified_baseline TEXT NOT NULL CHECK (length(verified_baseline) BETWEEN 40 AND 64),
				state TEXT NOT NULL CHECK (state IN ('active','revoked')),
				revision INTEGER NOT NULL CHECK (revision > 0),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				created_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				last_event_id TEXT NOT NULL UNIQUE REFERENCES events(event_id) ON DELETE RESTRICT,
				UNIQUE(vault_id, source_workspace_hash)
			) STRICT`,
			`CREATE UNIQUE INDEX workspace_mappings_active_local_idx ON workspace_mappings(vault_id, local_root_hash) WHERE state = 'active'`,
			`CREATE INDEX workspace_mappings_state_idx ON workspace_mappings(vault_id, state, workspace_id)`,
		},
	},
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	version, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("%w: database=%d application=%d", ErrUnsupportedSchema, version, currentSchemaVersion)
	}
	for _, candidate := range migrations {
		if candidate.version <= version {
			continue
		}
		if candidate.version != version+1 {
			return fmt.Errorf("sqlite event store migration sequence has a gap after version %d", version)
		}
		if err := applyMigration(ctx, db, candidate); err != nil {
			return err
		}
		version = candidate.version
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("sqlite event store migration stopped at version %d, expected %d", version, currentSchemaVersion)
	}
	return validateSchema(ctx, db)
}

func validateSchema(ctx context.Context, db *sql.DB) error {
	queries := []string{
		`SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at FROM events LIMIT 0`,
		`SELECT idempotency_key, event_id, request_hash FROM idempotency_keys LIMIT 0`,
		`SELECT vault_id, revision, status, retention_days, created_at, updated_at, last_event_id FROM vault_states LIMIT 0`,
		`SELECT artifact_id, vault_id, schema_version, sensitivity, content_type, size_bytes, content_hash, ciphertext_hash, storage_name, staging_name, state, created_at FROM artifacts LIMIT 0`,
		`SELECT task_id, vault_id, workspace_root_hash, baseline_commit, status, current_revision, created_at, updated_at, last_event_id, workspace_id FROM tasks LIMIT 0`,
		`SELECT task_id, revision, baseline_commit, created_at, event_id FROM task_contract_revisions LIMIT 0`,
		`SELECT decision_id, vault_id, task_id, question_revision, category, state, expected_repository_revision, created_at, updated_at, question_event_id, last_event_id FROM decisions LIMIT 0`,
		`SELECT answer_id, decision_id, question_revision, expected_repository_revision, answer_hash, created_at, event_id FROM decision_answers LIMIT 0`,
		`SELECT grant_id, vault_id, outcome, state, capability_hash, task_id, workspace_hash, created_at, expires_at, created_event_id, last_event_id FROM permission_grants LIMIT 0`,
		`SELECT run_id, vault_id, task_id, workspace_hash, state, created_at, updated_at, created_event_id, last_event_id FROM runs LIMIT 0`,
		`SELECT attempt_id, run_id, call_id, capability_hash, grant_id, state, exit_code, safe_error_code, created_at, updated_at, prepared_event_id, last_event_id FROM attempts LIMIT 0`,
		`SELECT request_id, vault_id, task_id, workspace_hash, capability_hash, state, created_at, updated_at, created_event_id, last_event_id FROM permission_requests LIMIT 0`,
		`SELECT evidence_id, vault_id, task_id, run_id, attempt_id, contract_revision, command_index, baseline_commit, worktree_state_hash, capability_hash, exit_code, started_at, finished_at, event_id FROM verification_evidence LIMIT 0`,
		`SELECT action_id, vault_id, task_id, kind, state, contract_revision, patch_hash, worktree_state_hash, evidence_id, safe_error_code, created_at, updated_at, created_event_id, last_event_id FROM patch_actions LIMIT 0`,
		`SELECT task_id, status, action_id, completed_at, event_id FROM task_outcomes LIMIT 0`,
		`SELECT vault_id, membership_id, state, revision, created_at, updated_at, last_event_id FROM account_links LIMIT 0`,
		`SELECT receipt_id, vault_id, task_id, contract_revision, provider_key, model_key, request_id, prompt_version, context_hash, request_hash, response_hash, provider_call_id, context_items, input_bytes, output_bytes, redaction_count, input_tokens, cached_input_tokens, output_tokens, status, safe_error_code, created_at, updated_at, created_event_id, last_event_id FROM model_egress_receipts LIMIT 0`,
		`SELECT memory_id, vault_id, kind, state, scope_kind, workspace_root_hash, sensitivity, confidence, revision, created_at, updated_at, reviewed_at, expires_at, superseded_by_memory_id, created_event_id, last_event_id FROM memory_records LIMIT 0`,
		`SELECT vault_id, device_id, public_key, state, revision, next_sequence, created_at, updated_at, last_event_id FROM sync_devices LIMIT 0`,
		`SELECT pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, ciphertext_hash, pack_envelope, state, received_at, event_id FROM sync_pack_receipts LIMIT 0`,
		`SELECT event_id, vault_id, device_id, device_seq, origin_kind FROM sync_event_origins LIMIT 0`,
		`SELECT vault_id, device_id, next_sequence, updated_at FROM sync_export_heads LIMIT 0`,
		`SELECT export_id, vault_id, device_id, sequence_start, sequence_end, event_count, state, pack_id, ciphertext_hash, pack_envelope, created_at, updated_at FROM sync_export_batches LIMIT 0`,
		`SELECT export_id, event_id, device_seq FROM sync_export_batch_events LIMIT 0`,
		`SELECT pack_id, vault_id, device_id, device_seq, event_id, event_hash, state, reason_code, recorded_at FROM sync_replay_items LIMIT 0`,
		`SELECT pack_id, vault_id, device_id, sequence_start, sequence_end, event_count, applied_count, conflicted_count, quarantined_count, completed_at, event_id FROM sync_replay_batches LIMIT 0`,
		`SELECT enrollment_id, vault_id, role, state, peer_device_id, offer_hash, acceptance_hash, acceptance_envelope, expires_at, created_at, updated_at, created_event_id, last_event_id FROM sync_enrollments LIMIT 0`,
		`SELECT workspace_id, vault_id, source_workspace_hash, local_root_hash, verified_baseline, state, revision, created_at, updated_at, created_event_id, last_event_id FROM workspace_mappings LIMIT 0`,
	}
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("validate sqlite event store schema: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close sqlite event store schema validation query: %w", err)
		}
	}
	return nil
}

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read sqlite event store schema version: %w", err)
	}
	return version, nil
}

func applyMigration(ctx context.Context, db *sql.DB, candidate migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite event store migration %d: %w", candidate.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range candidate.statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite event store migration %d: %w", candidate.version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", candidate.version)); err != nil {
		return fmt.Errorf("record sqlite event store migration %d: %w", candidate.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite event store migration %d: %w", candidate.version, err)
	}
	return nil
}
