package repopath

import "testing"

func TestNormalizeUsesPortableRepositorySemantics(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		value     string
		allowRoot bool
		want      string
	}{
		{name: "slash path", value: "internal/domain/task", want: "internal/domain/task"},
		{name: "backslash path", value: `internal\domain\task`, want: "internal/domain/task"},
		{name: "clean path", value: "internal/./task/../domain", want: "internal/domain"},
		{name: "repository root", value: ".", allowRoot: true, want: "."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := Normalize(testCase.value, testCase.allowRoot)
			if err != nil || got != testCase.want {
				t.Fatalf("Normalize(%q, %t) = %q, %v; want %q", testCase.value, testCase.allowRoot, got, err, testCase.want)
			}
		})
	}
}

func TestNormalizeRejectsEveryAbsoluteAndEscapingSyntax(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"/etc/passwd",
		`\windows\system32`,
		"C:/outside",
		`c:\outside`,
		"C:outside",
		`\\server\share\file`,
		"//server/share/file",
		`\\?\C:\outside`,
		"../outside",
		"inside/../../outside",
		".",
		"",
		"safe\x00unsafe",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if normalized, err := Normalize(value, false); err == nil {
				t.Fatalf("Normalize(%q, false) = %q; want rejection", value, normalized)
			}
		})
	}
}
