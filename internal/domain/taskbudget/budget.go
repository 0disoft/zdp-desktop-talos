package taskbudget

import (
	"errors"
	"time"
)

var ErrInvalidPolicy = errors.New("invalid task budget policy")

const (
	MaxModelCallsLimit  = 64
	MaxToolCallsLimit   = 256
	MaxTokenLimit       = 1 << 30
	MaxWallClockLimit   = 24 * time.Hour
	DefaultModelCalls   = 4
	DefaultToolCalls    = 16
	DefaultInputTokens  = 128 << 10
	DefaultOutputTokens = 16 << 10
	DefaultWallClock    = 30 * time.Minute
)

type Policy struct {
	MaxModelCalls   int
	MaxToolCalls    int
	MaxInputTokens  int
	MaxOutputTokens int
	MaxWallClock    time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		MaxModelCalls: DefaultModelCalls, MaxToolCalls: DefaultToolCalls,
		MaxInputTokens: DefaultInputTokens, MaxOutputTokens: DefaultOutputTokens,
		MaxWallClock: DefaultWallClock,
	}
}

func Normalize(policy Policy) (Policy, error) {
	if policy == (Policy{}) {
		policy = DefaultPolicy()
	}
	if policy.Validate() != nil {
		return Policy{}, ErrInvalidPolicy
	}
	return policy, nil
}

func (p Policy) Validate() error {
	if p.MaxModelCalls < 1 || p.MaxModelCalls > MaxModelCallsLimit ||
		p.MaxToolCalls < 1 || p.MaxToolCalls > MaxToolCallsLimit ||
		p.MaxInputTokens < 1 || p.MaxInputTokens > MaxTokenLimit ||
		p.MaxOutputTokens < 256 || p.MaxOutputTokens > MaxTokenLimit ||
		p.MaxWallClock <= 0 || p.MaxWallClock > MaxWallClockLimit {
		return ErrInvalidPolicy
	}
	return nil
}
