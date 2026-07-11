//go:build windows

package workerexec

import (
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processGroup struct{ job windows.Handle }

func newProcessGroup() (*processGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &processGroup{job: job}, nil
}

func (*processGroup) prepare(*exec.Cmd) error { return nil }
func (g *processGroup) attach(process *os.Process) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(g.job, handle)
}
func (g *processGroup) terminate() error { return windows.TerminateJobObject(g.job, 1) }
func (g *processGroup) close() {
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
