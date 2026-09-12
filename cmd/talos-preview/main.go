// Talos Preview runs an unsigned local build with a separate application profile.
// It is not a security sandbox and is never part of a signed release package.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type previewDiagnostic struct {
	Event     string `json:"event"`
	Stage     string `json:"stage"`
	ElapsedMS int64  `json:"elapsed_ms"`
	ExitCode  int    `json:"exit_code"`
	Timeout   bool   `json:"timeout"`
}

func writeDiagnostic(w io.Writer, event, stage string, started time.Time, code int, timeout bool) error {
	return json.NewEncoder(w).Encode(previewDiagnostic{event, stage, time.Since(started).Milliseconds(), code, timeout})
}

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

func run() (result error) {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	root := filepath.Dir(executable)
	// Only the configured, locally built sibling bundle is accepted.
	if filepath.Base(root) != "native-preview" || filepath.Base(filepath.Dir(root)) != ".tmp" {
		return fmt.Errorf("preview must remain in its .tmp/native-preview bundle")
	}
	// Keep only the latest launch; never copy child output or raw errors.
	logPath := filepath.Join(root, "preview-diagnostic.jsonl")
	if info, err := os.Lstat(logPath); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("preview diagnostic must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("preview diagnostic unavailable")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("preview diagnostic unavailable")
	}
	defer log.Close()
	started, stage, code := time.Now(), "bundle", -1
	var timedOut atomic.Bool
	if err := writeDiagnostic(log, "launcher_started", stage, started, code, false); err != nil {
		return fmt.Errorf("preview diagnostic write failed")
	}
	defer func() {
		event := "launcher_failed"
		if stage == "wait" {
			event = "app_exited"
		}
		if err := writeDiagnostic(log, event, stage, started, code, timedOut.Load()); err != nil && result == nil {
			result = fmt.Errorf("preview diagnostic write failed")
		}
	}()
	app := filepath.Join(root, "talos-desktop.exe")
	worker := filepath.Join(root, "talos-worker.exe")
	for _, path := range []string{app, worker} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("preview sibling binary missing")
		}
	}
	profile := filepath.Join(root, "profile")
	stage = "profile"
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
	stage = "start"
	if err := command.Start(); err != nil {
		return err
	}
	stage = "wait"
	_ = writeDiagnostic(log, "app_started", stage, started, code, false)
	// Bound forgotten preview sessions; normal window close exits immediately.
	timer := time.AfterFunc(15*time.Minute, func() { timedOut.Store(true); _ = command.Process.Kill() })
	defer timer.Stop()
	result = command.Wait()
	code = command.ProcessState.ExitCode()
	return result
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
