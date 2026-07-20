//go:build !windows

package workerexec

import (
	"os"
	"os/exec"
	"syscall"
)

type processGroup struct{ pid int }

func newProcessGroup() (*processGroup, error) { return &processGroup{}, nil }
func (*processGroup) prepare(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (g *processGroup) activate(process *os.Process) error { g.pid = process.Pid; return nil }
func (g *processGroup) terminate() error {
	if g.pid <= 0 {
		return nil
	}
	return syscall.Kill(-g.pid, syscall.SIGKILL)
}
func (*processGroup) close() {}
