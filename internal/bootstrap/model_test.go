package bootstrap

import "testing"

func TestEnvironmentModelFactoryFailsClosedByConfigurationState(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key-not-a-real-secret")
	t.Setenv(modelEnvironmentName, "")
	status := NewEnvironmentModelFactory(&ExecutionFactory{}).Status()
	if status.Ready || status.ReasonCode != "MODEL_NAME_UNCONFIGURED" || status.ModelKey != "" {
		t.Fatalf("missing model status=%+v", status)
	}

	t.Setenv(modelEnvironmentName, "Invalid Model Name")
	status = NewEnvironmentModelFactory(&ExecutionFactory{}).Status()
	if status.Ready || status.ReasonCode != "MODEL_CONFIGURATION_INVALID" {
		t.Fatalf("invalid model status=%+v", status)
	}

	t.Setenv(modelEnvironmentName, "model-test")
	t.Setenv("OPENAI_API_KEY", "")
	status = NewEnvironmentModelFactory(&ExecutionFactory{}).Status()
	if status.Ready || status.ReasonCode != "MODEL_CREDENTIAL_UNAVAILABLE" {
		t.Fatalf("missing credential status=%+v", status)
	}
}

func TestEnvironmentModelFactoryExposesOnlyCredentialHandle(t *testing.T) {
	const credential = "test-key-must-not-be-exposed"
	t.Setenv("OPENAI_API_KEY", credential)
	t.Setenv(modelEnvironmentName, "model-test")
	status := NewEnvironmentModelFactory(&ExecutionFactory{}).Status()
	if !status.Ready || status.ProviderKey != "openai-responses" || status.ModelKey != "model-test" || status.CredentialName != "OPENAI_API_KEY" || status.ReasonCode != "" {
		t.Fatalf("status=%+v", status)
	}
}
