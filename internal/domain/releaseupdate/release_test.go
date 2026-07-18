package releaseupdate

import "testing"

func TestCompareVersionsUsesNumericComponents(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"0.32.0", "0.33.0", -1},
		{"1.10.0", "1.2.9", 1},
		{"2.0.0", "2.0.0", 0},
	} {
		got, err := CompareVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
}

func TestParseVersionRejectsAmbiguousOrExtendedForms(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "v1.2.3", "1.2.3-beta", "-1.2.3"} {
		if _, err := ParseVersion(value); err == nil {
			t.Errorf("ParseVersion(%q) unexpectedly succeeded", value)
		}
	}
}
