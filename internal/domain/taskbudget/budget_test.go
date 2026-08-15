package taskbudget

import (
	"testing"
	"time"
)

func TestNormalizeUsesBoundedDefaultPolicy(t *testing.T) {
	policy, err := Normalize(Policy{})
	if err != nil || policy != DefaultPolicy() || policy.Validate() != nil {
		t.Fatalf("policy=%+v error=%v", policy, err)
	}
}

func TestPolicyRejectsUnboundedDimensions(t *testing.T) {
	tests := []Policy{
		{MaxModelCalls: 0, MaxToolCalls: 1, MaxInputTokens: 1, MaxOutputTokens: 256, MaxWallClock: time.Second},
		{MaxModelCalls: 1, MaxToolCalls: 0, MaxInputTokens: 1, MaxOutputTokens: 256, MaxWallClock: time.Second},
		{MaxModelCalls: 1, MaxToolCalls: 1, MaxInputTokens: 0, MaxOutputTokens: 256, MaxWallClock: time.Second},
		{MaxModelCalls: 1, MaxToolCalls: 1, MaxInputTokens: 1, MaxOutputTokens: 255, MaxWallClock: time.Second},
		{MaxModelCalls: 1, MaxToolCalls: 1, MaxInputTokens: 1, MaxOutputTokens: 256, MaxWallClock: 0},
	}
	for _, policy := range tests {
		if policy.Validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
}
