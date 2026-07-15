package redaction

import (
	"context"
	"regexp"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/secretscanner"
)

const replacement = "[REDACTED]"

type rule struct {
	pattern *regexp.Regexp
	replace string
}

type Scanner struct {
	rules []rule
}

func NewScanner() *Scanner {
	return &Scanner{rules: []rule{
		{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), replacement},
		{regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})`), replacement},
		{regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9_-]{16,}`), replacement},
		{regexp.MustCompile(`(?i)(https?://[^:/\s]+:)[^@/\s]+(@)`), `${1}` + replacement + `${2}`},
		{regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|client[_-]?secret)(\s*[:=]\s*["']?)([^"'\s,;]+)`), `${1}${2}` + replacement},
	}}
}

func (s *Scanner) Redact(ctx context.Context, text string) (secretscanner.Result, error) {
	if err := ctx.Err(); err != nil {
		return secretscanner.Result{}, err
	}
	lines := strings.Split(text, "\n")
	findings := 0
	inPrivateKey := false
	for index, line := range lines {
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "-----BEGIN ") && strings.Contains(upper, "PRIVATE KEY-----") {
			inPrivateKey = true
		}
		if inPrivateKey {
			prefix := ""
			if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
				prefix = line[:1]
			}
			lines[index] = prefix + replacement
			findings++
			if strings.Contains(upper, "-----END ") && strings.Contains(upper, "PRIVATE KEY-----") {
				inPrivateKey = false
			}
			continue
		}
		for _, candidate := range s.rules {
			matches := candidate.pattern.FindAllStringIndex(lines[index], -1)
			if len(matches) == 0 {
				continue
			}
			findings += len(matches)
			lines[index] = candidate.pattern.ReplaceAllString(lines[index], candidate.replace)
		}
	}
	return secretscanner.Result{Text: strings.Join(lines, "\n"), Findings: findings}, nil
}
