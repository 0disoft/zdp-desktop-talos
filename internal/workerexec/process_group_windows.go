//go:build windows

package workerexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
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

func (*processGroup) prepare(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return nil
}

func (g *processGroup) activate(process *os.Process) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(g.job, handle); err != nil {
		return err
	}
	threadID, err := soleProcessThreadID(uint32(process.Pid))
	if err != nil {
		return err
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	previousCount, err := windows.ResumeThread(thread)
	if err != nil {
		return err
	}
	if previousCount != 1 {
		return fmt.Errorf("unexpected initial thread suspension count: %d", previousCount)
	}
	return nil
}

func soleProcessThreadID(processID uint32) (uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return 0, err
	}
	var threadID uint32
	for {
		if entry.OwnerProcessID == processID {
			if threadID != 0 {
				return 0, errors.New("suspended process has multiple initial threads")
			}
			threadID = entry.ThreadID
		}
		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if threadID == 0 {
		return 0, errors.New("suspended process has no initial thread")
	}
	return threadID, nil
}
func (g *processGroup) terminate() error { return windows.TerminateJobObject(g.job, 1) }
func (g *processGroup) close() {
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
