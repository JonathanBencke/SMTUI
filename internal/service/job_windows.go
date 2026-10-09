package service

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processJob wraps a Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. Every process launched by a service
// (build, run, generate-sources) is assigned to one, and its descendants
// inherit it automatically. That gives two guarantees taskkill /T cannot:
//
//   - terminate reaches every descendant, even those whose intermediate parent
//     already died (taskkill walks the parent PID chain and misses them);
//   - if smtui itself dies (crash, console window closed, kill), the OS closes
//     the job handle and kills the whole tree, so nothing is left orphaned.
type processJob struct {
	mu     sync.Mutex
	handle windows.Handle
}

// newProcessJob creates a kill-on-close job and assigns the process pid to it.
// The handle is not inheritable, so only smtui holds it.
func newProcessJob(pid int) (*processJob, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("configure job object: %w", err)
	}

	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(proc)

	if err := windows.AssignProcessToJobObject(handle, proc); err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("assign process %d to job: %w", pid, err)
	}

	return &processJob{handle: handle}, nil
}

// close terminates every process still in the job and releases the handle.
// It is idempotent and safe on a nil receiver.
func (j *processJob) close() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return
	}
	_ = windows.TerminateJobObject(j.handle, 1)
	_ = windows.CloseHandle(j.handle)
	j.handle = 0
}
