// Talos Preview runs an unsigned local build with a separate application profile.
// It is not a security sandbox and is never part of a signed release package.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func previewEnvironment(profile string, environ []string) []string {
	allowed := map[string]bool{"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true, "USERPROFILE": true}
	result := []string{}
	for _, value := range environ {
		key, _, ok := strings.Cut(value, "=")
		if ok && allowed[strings.ToUpper(key)] {
			result = append(result, value)
		}
	}
	return append(result, "LOCALAPPDATA="+profile, "APPDATA="+filepath.Join(profile, "roaming"), "TEMP="+filepath.Join(profile, "temp"), "TMP="+filepath.Join(profile, "temp"), "WEBVIEW2_USER_DATA_FOLDER="+filepath.Join(profile, "webview"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=NUL")
}

func run() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	// Only the configured, locally built sibling bundle is accepted.
	if filepath.Base(root) != "native-preview" || filepath.Base(filepath.Dir(root)) != ".tmp" {
		return fmt.Errorf("preview must remain in its .tmp/native-preview bundle")
	}
	app := filepath.Join(root, "talos-desktop.exe")
	worker := filepath.Join(root, "talos-worker.exe")
	for _, path := range []string{app, worker} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("preview sibling binary missing")
		}
	}
	profile := filepath.Join(root, "profile")
	for _, path := range []string{profile, filepath.Join(profile, "roaming"), filepath.Join(profile, "temp"), filepath.Join(profile, "webview")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("preview profile must be a local directory")
		}
	}
	command := exec.Command(app)
	command.Dir = root
	command.Env = previewEnvironment(profile, os.Environ())
	if err := command.Start(); err != nil {
		return err
	}
	// Bound forgotten preview sessions; normal window close exits immediately.
	timer := time.AfterFunc(15*time.Minute, func() { _ = command.Process.Kill() })
	defer timer.Stop()
	return command.Wait()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
