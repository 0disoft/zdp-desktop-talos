package permissionbroker

import (
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
)

var (
	ErrInvalidPolicy               = errors.New("invalid permission broker policy")
	ErrVerificationRuleUnavailable = errors.New("verification rule is unavailable")
)

type ProcessRule struct {
	ID               string
	Executable       string
	ArgumentPrefix   []string
	MaxArguments     int
	EnvironmentNames []string
	Environment      map[string]string
	Effects          []string
	MaxTimeout       time.Duration
	MaxOutputBytes   int
	Default          permission.Outcome
}

type Evaluation struct {
	Outcome        permission.Outcome
	ReasonCode     string
	Capability     *permission.ProcessCapability
	MatchedGrantID string
	ConsumesGrant  bool
}

type ResolvedVerification struct {
	Intent           permission.ProcessIntent
	Environment      map[string]string
	WorkingDirectory string
}

type Broker struct {
	rules map[string]ProcessRule
	now   func() time.Time
}

func New(rules []ProcessRule) (*Broker, error) {
	if len(rules) == 0 || len(rules) > 64 {
		return nil, ErrInvalidPolicy
	}
	indexed := make(map[string]ProcessRule, len(rules))
	for _, rule := range rules {
		if rule.MaxArguments == 0 {
			rule.MaxArguments = len(rule.ArgumentPrefix)
		}
		if rule.ID == "" || len(rule.ID) > 128 || !filepath.IsAbs(rule.Executable) || isShell(rule.Executable) || len(rule.ArgumentPrefix) == 0 || len(rule.ArgumentPrefix) > 256 || rule.MaxArguments < len(rule.ArgumentPrefix) || rule.MaxArguments > 256 || len(rule.EnvironmentNames) > 32 || rule.MaxTimeout <= 0 || rule.MaxTimeout > 10*time.Minute || rule.MaxOutputBytes <= 0 || rule.MaxOutputBytes > 4<<20 || (rule.Default != permission.OutcomeDeny && rule.Default != permission.OutcomeRequireReview && rule.Default != permission.OutcomeAllowTask) {
			return nil, ErrInvalidPolicy
		}
		if _, exists := indexed[rule.ID]; exists {
			return nil, ErrInvalidPolicy
		}
		seen := map[string]struct{}{}
		for _, name := range rule.EnvironmentNames {
			key := envKey(name)
			if !validEnvName(name) {
				return nil, ErrInvalidPolicy
			}
			if _, exists := seen[key]; exists {
				return nil, ErrInvalidPolicy
			}
			seen[key] = struct{}{}
		}
		for name, value := range rule.Environment {
			if _, exists := seen[envKey(name)]; !exists || strings.IndexByte(value, 0) >= 0 || len(value) > 32767 {
				return nil, ErrInvalidPolicy
			}
		}
		effects := map[string]struct{}{}
		for _, effect := range rule.Effects {
			if !validEffect(effect) {
				return nil, ErrInvalidPolicy
			}
			if _, exists := effects[effect]; exists {
				return nil, ErrInvalidPolicy
			}
			effects[effect] = struct{}{}
		}
		indexed[rule.ID] = rule
	}
	return &Broker{rules: indexed, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (b *Broker) ResolveVerification(record task.Record, command task.VerificationCommand) (ResolvedVerification, error) {
	if b == nil || record.Validate() != nil {
		return ResolvedVerification{}, ErrVerificationRuleUnavailable
	}
	normalized, err := command.Normalize()
	if err != nil {
		return ResolvedVerification{}, ErrVerificationRuleUnavailable
	}
	rule, exists := b.rules[normalized.RuleID]
	if !exists || !prefixMatches(rule.ArgumentPrefix, normalized.Arguments) || len(normalized.Arguments) > rule.MaxArguments {
		return ResolvedVerification{}, ErrVerificationRuleUnavailable
	}
	intent := permission.ProcessIntent{
		TaskID: record.ID, WorkspaceRoot: record.WorkspaceRoot, RuleID: rule.ID, Executable: rule.Executable,
		Arguments: append([]string(nil), normalized.Arguments...), EnvironmentNames: environmentNames(rule.Environment), Timeout: rule.MaxTimeout, MaxOutputBytes: rule.MaxOutputBytes,
	}
	if err := intent.Validate(); err != nil {
		return ResolvedVerification{}, ErrVerificationRuleUnavailable
	}
	return ResolvedVerification{Intent: intent, Environment: copyEnvironment(rule.Environment), WorkingDirectory: normalized.WorkingDirectory}, nil
}

func (b *Broker) Evaluate(record task.Record, contract task.ContractRevision, intent permission.ProcessIntent, grants []permission.Grant) Evaluation {
	if record.Validate() != nil || contract.Validate() != nil || intent.Validate() != nil || record.ID != contract.TaskID || record.ID != intent.TaskID || record.WorkspaceRoot != intent.WorkspaceRoot || record.BaselineCommit != contract.BaselineCommit {
		return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "PERMISSION_CONTEXT_INVALID"}
	}
	rule, exists := b.rules[intent.RuleID]
	if !exists || canonicalPath(rule.Executable) != canonicalPath(intent.Executable) || !prefixMatches(rule.ArgumentPrefix, intent.Arguments) || len(intent.Arguments) > rule.MaxArguments || intent.Timeout > rule.MaxTimeout || intent.MaxOutputBytes > rule.MaxOutputBytes || !environmentSubset(intent.EnvironmentNames, rule.EnvironmentNames) {
		return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "PROCESS_RULE_MISMATCH"}
	}
	if contractForbids(contract, intent.RuleID) {
		return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "TASK_CONTRACT_FORBIDS_PROCESS"}
	}
	if contractForbidsEffects(contract, rule.Effects) {
		return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "TASK_CONTRACT_FORBIDS_EFFECT"}
	}
	hash, err := permission.IntentHash(intent)
	if err != nil {
		return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "PERMISSION_INTENT_INVALID"}
	}
	workspaceHash, _ := permission.WorkspaceHash(record.WorkspaceRoot)
	matching := make([]permission.Grant, 0)
	for _, grant := range grants {
		if grant.Validate() != nil || grant.State != permission.GrantActive || grant.WorkspaceHash != workspaceHash || (!grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(b.now())) {
			continue
		}
		if grant.TaskID != "" && grant.TaskID != record.ID {
			continue
		}
		expectedHash := hash
		if grant.Outcome == permission.OutcomeAllowWorkspace || (grant.Outcome == permission.OutcomeDeny && grant.TaskID == "") {
			expectedHash, err = permission.WorkspaceIntentHash(intent)
			if err != nil {
				continue
			}
		}
		if grant.CapabilityHash != expectedHash {
			continue
		}
		matching = append(matching, grant)
	}
	sort.Slice(matching, func(i, j int) bool {
		leftPriority, rightPriority := grantPriority(matching[i]), grantPriority(matching[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return matching[i].ID < matching[j].ID
	})
	if len(matching) > 0 {
		grant := matching[0]
		if grant.Outcome == permission.OutcomeDeny {
			return Evaluation{Outcome: permission.OutcomeDeny, ReasonCode: "EXPLICIT_GRANT_DENY", MatchedGrantID: grant.ID}
		}
		return allowEvaluation(grant.Outcome, "EXPLICIT_GRANT_ALLOW", hash, rule, intent, grant.ID, grant.Outcome == permission.OutcomeAllowOnce)
	}
	if rule.Default == permission.OutcomeAllowTask {
		return allowEvaluation(rule.Default, "PRODUCT_RULE_ALLOW", hash, rule, intent, "", false)
	}
	return Evaluation{Outcome: rule.Default, ReasonCode: map[permission.Outcome]string{permission.OutcomeDeny: "PRODUCT_RULE_DENY", permission.OutcomeRequireReview: "USER_REVIEW_REQUIRED"}[rule.Default]}
}

func allowEvaluation(outcome permission.Outcome, reason, hash string, rule ProcessRule, intent permission.ProcessIntent, grantID string, consumes bool) Evaluation {
	capability := &permission.ProcessCapability{ID: rule.ID + ":" + hash[:16], Executable: rule.Executable, ArgumentPrefix: append([]string(nil), intent.Arguments...), MaxArguments: len(intent.Arguments), EnvironmentNames: append([]string(nil), intent.EnvironmentNames...), MaxTimeout: intent.Timeout, MaxOutputBytes: intent.MaxOutputBytes, IntentHash: hash}
	return Evaluation{Outcome: outcome, ReasonCode: reason, Capability: capability, MatchedGrantID: grantID, ConsumesGrant: consumes}
}
func contractForbids(contract task.ContractRevision, ruleID string) bool {
	for _, action := range contract.ForbiddenActions {
		if action == "process.exec" || action == "process.exec:"+ruleID {
			return true
		}
	}
	return false
}
func contractForbidsEffects(contract task.ContractRevision, effects []string) bool {
	for _, forbidden := range contract.ForbiddenActions {
		for _, effect := range effects {
			if forbidden == effect {
				return true
			}
		}
	}
	return false
}
func environmentNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return envKey(names[i]) < envKey(names[j]) })
	return names
}
func copyEnvironment(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
func prefixMatches(prefix, arguments []string) bool {
	if len(arguments) < len(prefix) {
		return false
	}
	for i := range prefix {
		if prefix[i] != arguments[i] {
			return false
		}
	}
	return true
}
func environmentSubset(requested, allowed []string) bool {
	set := map[string]struct{}{}
	for _, name := range allowed {
		set[envKey(name)] = struct{}{}
	}
	for _, name := range requested {
		if _, ok := set[envKey(name)]; !ok {
			return false
		}
	}
	return true
}
func grantPriority(grant permission.Grant) int {
	if grant.Outcome == permission.OutcomeDeny {
		return 0
	}
	switch grant.Outcome {
	case permission.OutcomeAllowOnce:
		return 1
	case permission.OutcomeAllowTask:
		return 2
	default:
		return 3
	}
}
func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
func validEnvName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, "=") {
		return false
	}
	for _, char := range name {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
func validEffect(effect string) bool {
	switch effect {
	case "network.egress", "dependency.install":
		return true
	default:
		return false
	}
}
func isShell(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "cmd.exe", "cmd", "powershell.exe", "powershell", "pwsh.exe", "pwsh", "sh", "bash", "zsh", "fish":
		return true
	}
	return false
}
