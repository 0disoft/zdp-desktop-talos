package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewEnvironmentIsolatesProfileAndDropsProviderCredentials(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	got := previewEnvironment(profile, []string{"PATH=tools", "SYSTEMROOT=system", "LOCALAPPDATA=real-profile", "APPDATA=real-roaming", "OPENAI_API_KEY=must-not-inherit", "ANTHROPIC_API_KEY=must-not-inherit", "GITHUB_TOKEN=must-not-inherit", "WEBVIEW2_USER_DATA_FOLDER=real-webview"})
	values := map[string]string{}
	for _, entry := range got {
		k, v, _ := strings.Cut(entry, "=")
		values[k] = v
	}
	if values["LOCALAPPDATA"] != profile || values["APPDATA"] != filepath.Join(profile, "roaming") || values["TEMP"] != filepath.Join(profile, "temp") || values["WEBVIEW2_USER_DATA_FOLDER"] != filepath.Join(profile, "webview") {
		t.Fatalf("profile isolation failed: %v", values)
	}
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN"} {
		if _, ok := values[key]; ok {
			t.Fatalf("inherited %s", key)
		}
	}
	if values["PATH"] != "tools" || values["SYSTEMROOT"] != "system" {
		t.Fatal("required OS environment lost")
	}
}
