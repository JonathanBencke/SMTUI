package service

import (
	"fmt"
	"os/exec"
	"syscall"
)

// execution is one process launched by a service — a synchronous step (build
// or generate-sources) or the long-running run process — together with the
// job object that owns its whole process tree.
type execution struct {
	cmd *exec.Cmd
	job *processJob
	// done is closed by finish, once the process has exited and its job (and
	// therefore every leftover descendant) has been terminated.
	done chan struct{}
}

// launch starts cmd and attaches it to a kill-on-close job object. Failing to
// create the job is not fatal: the process still runs and kill falls back to
// taskkill /T, but the failure is logged since that path can leak orphans.
func (s *Service) launch(cmd *exec.Cmd) (*execution, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ex := &execution{cmd: cmd, done: make(chan struct{})}
	job, err := newProcessJob(cmd.Process.Pid)
	if err != nil {
		s.emitLog(fmt.Sprintf("Warning: PID %d is not tracked by a job object (%v); stop falls back to taskkill", cmd.Process.Pid, err))
	} else {
		ex.job = job
	}
	return ex, nil
}

func (ex *execution) pid() int { return ex.cmd.Process.Pid }

// kill force-kills the whole process tree. taskkill /T runs first to catch
// any descendant spawned before the job was assigned (still linked by parent
// PID); terminating the job then catches descendants whose parent already
// died, which taskkill cannot reach.
func (ex *execution) kill() {
	taskkill := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", ex.pid()))
	taskkill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	_, _ = taskkill.CombinedOutput()

	ex.job.close()
	_ = ex.cmd.Process.Kill()
}

// finish is called exactly once, by the goroutine that waited for the
// process: it kills any descendant the process left behind and signals done.
func (ex *execution) finish() {
	ex.job.close()
	close(ex.done)
}
