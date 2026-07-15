package secretscanner

import "context"

type Result struct {
	Text     string
	Findings int
}

type Scanner interface {
	Redact(context.Context, string) (Result, error)
}
