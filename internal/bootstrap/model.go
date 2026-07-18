package bootstrap

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/openairesponses"
	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

const modelEnvironmentName = "TALOS_OPENAI_MODEL"

var ErrModelRuntimeUnavailable = errors.New("model runtime is unavailable")

type ModelStatus struct {
	ProviderKey    string
	ModelKey       string
	CredentialName string
	Ready          bool
	ReasonCode     string
}

type ModelFactory struct {
	provider  modelprovider.Provider
	execution *ExecutionFactory
	policy    modelruntime.Policy
	status    ModelStatus
}

func NewEnvironmentModelFactory(execution *ExecutionFactory) *ModelFactory {
	status := ModelStatus{ProviderKey: openairesponses.ProviderKey, CredentialName: "OPENAI_API_KEY"}
	modelKey, modelPresent := os.LookupEnv(modelEnvironmentName)
	status.ModelKey = strings.TrimSpace(modelKey)
	credentialValue, credentialPresent := os.LookupEnv("OPENAI_API_KEY")
	credentialPresent = credentialPresent && strings.TrimSpace(credentialValue) != "" && !strings.ContainsAny(credentialValue, "\r\n")
	switch {
	case execution == nil:
		status.ReasonCode = "MODEL_EXECUTION_UNAVAILABLE"
	case !modelPresent || status.ModelKey == "":
		status.ReasonCode = "MODEL_NAME_UNCONFIGURED"
	case !planning.ValidKey(status.ModelKey):
		status.ReasonCode = "MODEL_CONFIGURATION_INVALID"
	case !credentialPresent:
		status.ReasonCode = "MODEL_CREDENTIAL_UNAVAILABLE"
	default:
		status.Ready = true
	}
	factory := &ModelFactory{execution: execution, status: status}
	if !status.Ready {
		return factory
	}
	provider, err := openairesponses.New(openairesponses.EnvironmentCredential{}, &http.Client{})
	if err != nil {
		factory.status.Ready = false
		factory.status.ReasonCode = "MODEL_PROVIDER_UNAVAILABLE"
		return factory
	}
	factory.provider = provider
	factory.policy = modelruntime.Policy{
		ProviderKey: openairesponses.ProviderKey, ModelKey: status.ModelKey, PromptVersion: modelruntime.PlanningPromptVersion,
		MaxContextItems: 16, MaxMemoryCandidates: 200, MaxMemoryItems: 8, MaxMemoryBytes: 32 << 10,
		MaxInputBytes: 128 << 10, MaxOutputBytes: 32 << 10, MaxSteps: 8, MaxToolIntents: 8,
		AllowSensitive: false, ProviderTimeout: 60 * time.Second,
	}
	return factory
}

func (f *ModelFactory) Status() ModelStatus {
	if f == nil {
		return ModelStatus{ProviderKey: openairesponses.ProviderKey, CredentialName: "OPENAI_API_KEY", ReasonCode: "MODEL_PROVIDER_UNAVAILABLE"}
	}
	return f.status
}

func (f *ModelFactory) ProviderConfiguration() (providerKey, modelKey, credentialName, reasonCode string, ready bool) {
	status := f.Status()
	return status.ProviderKey, status.ModelKey, status.CredentialName, status.ReasonCode, status.Ready
}

func (f *ModelFactory) New(store vaultbootstrap.PlanningDatabase) (modelruntime.Proposer, error) {
	if f == nil || !f.status.Ready || f.provider == nil || f.execution == nil || store == nil {
		return nil, ErrModelRuntimeUnavailable
	}
	executor, err := f.execution.New(store)
	if err != nil {
		return nil, errors.Join(ErrModelRuntimeUnavailable, err)
	}
	memories, err := contextassembly.New(store)
	if err != nil {
		return nil, errors.Join(ErrModelRuntimeUnavailable, err)
	}
	service, err := modelruntime.New(store, f.provider, redaction.NewScanner(), executor, f.policy, modelruntime.WithMemoryContext(memories))
	if err != nil {
		return nil, errors.Join(ErrModelRuntimeUnavailable, err)
	}
	return service, nil
}
