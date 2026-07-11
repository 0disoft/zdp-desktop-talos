package workerexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxOutputBytes  = 1 << 20
	AbsoluteMaxOutputBytes = 4 << 20
	DefaultTimeout         = 2 * time.Minute
	AbsoluteMaxTimeout     = 10 * time.Minute
	MaxArguments           = 256
	MaxEnvironmentEntries  = 32
)

var (
	ErrInvalidPolicy     = errors.New("invalid process execution policy")
	ErrInvalidRequest    = errors.New("invalid process execution request")
	ErrCapabilityDenied  = errors.New("process capability denied")
	ErrPathEscape        = errors.New("process working directory escapes task worktree")
	ErrEnvironmentDenied = errors.New("process environment variable denied")
	ErrOutputLimit       = errors.New("process output limit exceeded")
	ErrCanceled          = errors.New("process execution canceled")
	ErrTimedOut          = errors.New("process execution timed out")
	ErrStartFailed       = errors.New("process failed to start")
	ErrContainmentFailed = errors.New("process tree containment failed")
)

type Capability struct {
	ID               string
	Executable       string
	ArgumentPrefix   []string
	MaxArguments     int
	EnvironmentNames []string
	MaxTimeout       time.Duration
	MaxOutputBytes   int
}

type Policy struct {
	WorktreeRoot string
	Capabilities []Capability
}

type Request struct {
	CapabilityID     string
	Arguments        []string
	WorkingDirectory string
	Environment      map[string]string
	Timeout          time.Duration
	MaxOutputBytes   int
}

type Result struct {
	ExitCode   int
	Stdout     []byte
	Stderr     []byte
	StartedAt  time.Time
	FinishedAt time.Time
}

type Executor struct {
	root         string
	capabilities map[string]Capability
	now          func() time.Time
}

func New(policy Policy) (*Executor, error) {
	root, err := canonicalDirectory(policy.WorktreeRoot)
	if err != nil || len(policy.Capabilities) == 0 {
		return nil, ErrInvalidPolicy
	}
	capabilities := make(map[string]Capability, len(policy.Capabilities))
	for _, capability := range policy.Capabilities {
		if capability.ID == "" || len(capability.ID) > 128 || strings.ContainsAny(capability.ID, "\x00\r\n\t") || len(capability.ArgumentPrefix) > MaxArguments || len(capability.EnvironmentNames) > MaxEnvironmentEntries {
			return nil, ErrInvalidPolicy
		}
		executable, err := canonicalExecutable(capability.Executable)
		if err != nil || isShellExecutable(executable) {
			return nil, ErrInvalidPolicy
		}
		if _, exists := capabilities[capability.ID]; exists {
			return nil, ErrInvalidPolicy
		}
		if capability.MaxArguments == 0 {
			capability.MaxArguments = len(capability.ArgumentPrefix)
		}
		if capability.MaxArguments < len(capability.ArgumentPrefix) || capability.MaxArguments > MaxArguments {
			return nil, ErrInvalidPolicy
		}
		if capability.MaxTimeout <= 0 {
			capability.MaxTimeout = DefaultTimeout
		}
		if capability.MaxTimeout > AbsoluteMaxTimeout {
			return nil, ErrInvalidPolicy
		}
		if capability.MaxOutputBytes <= 0 {
			capability.MaxOutputBytes = DefaultMaxOutputBytes
		}
		if capability.MaxOutputBytes > AbsoluteMaxOutputBytes {
			return nil, ErrInvalidPolicy
		}
		capability.Executable = executable
		allowed := make(map[string]struct{}, len(capability.EnvironmentNames))
		for _, name := range capability.EnvironmentNames {
			if !validEnvironmentName(name) {
				return nil, ErrInvalidPolicy
			}
			key := normalizeEnvironmentName(name)
			if _, exists := allowed[key]; exists {
				return nil, ErrInvalidPolicy
			}
			allowed[key] = struct{}{}
		}
		capabilities[capability.ID] = capability
	}
	return &Executor{root: root, capabilities: capabilities, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (e *Executor) Execute(ctx context.Context, request Request) (Result, error) {
	capability, ok := e.capabilities[request.CapabilityID]
	if !ok {
		return Result{}, ErrCapabilityDenied
	}
	if len(request.Arguments) < len(capability.ArgumentPrefix) || len(request.Arguments) > capability.MaxArguments {
		return Result{}, ErrInvalidRequest
	}
	for index, expected := range capability.ArgumentPrefix {
		if request.Arguments[index] != expected {
			return Result{}, ErrCapabilityDenied
		}
	}
	for _, argument := range request.Arguments {
		if strings.IndexByte(argument, 0) >= 0 || len(argument) > 32767 {
			return Result{}, ErrInvalidRequest
		}
	}
	cwd, err := e.workingDirectory(request.WorkingDirectory)
	if err != nil {
		return Result{}, err
	}
	environment, err := executionEnvironment(capability, request.Environment)
	if err != nil {
		return Result{}, err
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = capability.MaxTimeout
	}
	if timeout > capability.MaxTimeout || timeout > AbsoluteMaxTimeout {
		return Result{}, ErrCapabilityDenied
	}
	outputLimit := request.MaxOutputBytes
	if outputLimit <= 0 {
		outputLimit = capability.MaxOutputBytes
	}
	if outputLimit > capability.MaxOutputBytes || outputLimit > AbsoluteMaxOutputBytes {
		return Result{}, ErrCapabilityDenied
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.Command(capability.Executable, request.Arguments...)
	command.Dir, command.Env, command.Stdin = cwd, environment, nil
	limitReached := make(chan struct{}, 1)
	budget := newOutputBudget(outputLimit, limitReached)
	stdout := &limitedOutput{budget: budget}
	stderr := &limitedOutput{budget: budget}
	command.Stdout, command.Stderr = stdout, stderr
	group, err := newProcessGroup()
	if err != nil {
		return Result{}, fmt.Errorf("%w: initialize", ErrContainmentFailed)
	}
	defer group.close()
	if err := group.prepare(command); err != nil {
		return Result{}, fmt.Errorf("%w: prepare", ErrContainmentFailed)
	}
	result := Result{ExitCode: -1, StartedAt: e.now()}
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrStartFailed, safeStartError(err))
	}
	if err := group.attach(command.Process); err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		return Result{}, fmt.Errorf("%w: attach", ErrContainmentFailed)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var waitErr error
	var executionErr error
	select {
	case waitErr = <-done:
	case <-limitReached:
		_ = group.terminate()
		waitErr = <-done
		executionErr = ErrOutputLimit
	case <-commandCtx.Done():
		_ = group.terminate()
		waitErr = <-done
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			executionErr = ErrTimedOut
		} else {
			executionErr = ErrCanceled
		}
	}
	result.FinishedAt = e.now()
	result.Stdout, result.Stderr = stdout.bytes(), stderr.bytes()
	if waitErr == nil {
		result.ExitCode = 0
	} else {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			result.ExitCode = exitError.ExitCode()
		}
	}
	if executionErr != nil {
		return result, executionErr
	}
	return result, nil
}

func (e *Executor) workingDirectory(relative string) (string, error) {
	if relative == "" {
		return e.root, nil
	}
	if strings.IndexByte(relative, 0) >= 0 || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", ErrPathEscape
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	candidate, err := canonicalDirectory(filepath.Join(e.root, clean))
	if err != nil || !containsPath(e.root, candidate) {
		return "", ErrPathEscape
	}
	return candidate, nil
}

func executionEnvironment(capability Capability, requested map[string]string) ([]string, error) {
	if len(requested) > MaxEnvironmentEntries {
		return nil, ErrEnvironmentDenied
	}
	allowed := make(map[string]struct{}, len(capability.EnvironmentNames))
	for _, name := range capability.EnvironmentNames {
		allowed[normalizeEnvironmentName(name)] = struct{}{}
	}
	values := make(map[string]string)
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE"} {
		if value := os.Getenv(name); value != "" {
			values[normalizeEnvironmentName(name)] = name + "=" + value
		}
	}
	for name, value := range requested {
		key := normalizeEnvironmentName(name)
		if _, ok := allowed[key]; !ok || !validEnvironmentName(name) || strings.IndexByte(value, 0) >= 0 || len(value) > 32767 {
			return nil, ErrEnvironmentDenied
		}
		values[key] = name + "=" + value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, values[key])
	}
	return environment, nil
}

func canonicalExecutable(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", ErrInvalidPolicy
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", ErrInvalidPolicy
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", ErrInvalidPolicy
	}
	return filepath.Clean(resolved), nil
}

func canonicalDirectory(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", ErrPathEscape
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", ErrPathEscape
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", ErrPathEscape
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", ErrPathEscape
	}
	return filepath.Clean(resolved), nil
}

func containsPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func normalizeEnvironmentName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
func validEnvironmentName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, "=") {
		return false
	}
	for _, char := range name {
		if !(char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
func isShellExecutable(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	switch name {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe", "sh", "sh.exe", "bash", "bash.exe", "zsh", "zsh.exe", "fish", "fish.exe":
		return true
	}
	return false
}
func safeStartError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if errors.Is(err, os.ErrPermission) {
		return os.ErrPermission
	}
	return errors.New("unclassified start failure")
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	exceeded  bool
	signal    chan<- struct{}
}

func newOutputBudget(limit int, signal chan<- struct{}) *outputBudget {
	return &outputBudget{remaining: limit, signal: signal}
}

type limitedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	budget *outputBudget
}

func (o *limitedOutput) Write(payload []byte) (int, error) {
	o.budget.mu.Lock()
	accepted := len(payload)
	if accepted > o.budget.remaining {
		accepted = o.budget.remaining
	}
	o.budget.remaining -= accepted
	if accepted < len(payload) && !o.budget.exceeded {
		o.budget.exceeded = true
		select {
		case o.budget.signal <- struct{}{}:
		default:
		}
	}
	o.budget.mu.Unlock()
	o.mu.Lock()
	if accepted > 0 {
		_, _ = o.buffer.Write(payload[:accepted])
	}
	o.mu.Unlock()
	return len(payload), nil
}
func (o *limitedOutput) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.buffer.Bytes()...)
}
