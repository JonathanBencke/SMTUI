package service

import (
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"golang.org/x/sys/windows"
)

// longRunning keeps a process alive long enough for a test to act on it.
const longRunning = "cmd /c ping -n 30 127.0.0.1"

// jobProcessIDs lists the PIDs currently assigned to job.
func jobProcessIDs(t *testing.T, job *processJob) []uint32 {
	t.Helper()
	var list struct {
		Assigned uint32
		InList   uint32
		IDs      [64]uintptr
	}
	if err := windows.QueryInformationJobObject(job.handle, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil); err != nil {
		t.Fatalf("QueryInformationJobObject: %v", err)
	}
	ids := make([]uint32, 0, list.InList)
	for i := uint32(0); i < list.InList; i++ {
		ids = append(ids, uint32(list.IDs[i]))
	}
	return ids
}

// waitProcessGone reports whether pid exits within timeout.
func waitProcessGone(pid uint32, timeout time.Duration) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return true // already gone
	}
	defer windows.CloseHandle(h)
	ev, _ := windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	return ev == windows.WAIT_OBJECT_0
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStart_ConcurrentCallsLaunchSingleProcess(t *testing.T) {
	s := New(config.ServiceConfig{Name: "svc", Workdir: t.TempDir()}, config.Defaults{}, config.Preset{Run: longRunning}, "")
	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Start() }()
	}
	wg.Wait()
	close(errs)
	defer s.Stop()

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Errorf("successful Start() calls = %d, want exactly 1 (duplicates would be orphaned)", succeeded)
	}
	if got := strings.Count(strings.Join(s.Logs(), "\n"), "Starting ("); got != 1 {
		t.Errorf("run process launched %d times, want 1", got)
	}
}

func TestStop_DuringBuild_KillsBuildAndNeverLaunchesRun(t *testing.T) {
	preset := config.Preset{Build: longRunning, Run: "cmd /c echo run-must-not-start"}
	s := New(config.ServiceConfig{Name: "svc", Workdir: t.TempDir()}, config.Defaults{}, preset, "")
	startErr := make(chan error, 1)
	go func() { startErr <- s.Start() }()
	var build *execution
	waitFor(t, "build step to be tracked", 10*time.Second, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		build = s.step
		return build != nil
	})

	err := s.Stop()

	if err != nil {
		t.Fatalf("Stop() during build error = %v", err)
	}
	if got := <-startErr; got != errStartCancelled {
		t.Errorf("Start() error = %v, want errStartCancelled", got)
	}
	if got := s.Status(); got != StatusStopped {
		t.Errorf("Status() = %q, want %q", got, StatusStopped)
	}
	if !waitProcessGone(uint32(build.pid()), 5*time.Second) {
		t.Errorf("build process PID %d still alive after Stop()", build.pid())
	}
	if logs := strings.Join(s.Logs(), "\n"); strings.Contains(logs, "run-must-not-start") || strings.Contains(logs, "Starting (") {
		t.Errorf("run step must not start after a stop during build:\n%s", logs)
	}
}

func TestStop_ThenStartDoesNotLeaveTwoProcesses(t *testing.T) {
	preset := config.Preset{Build: longRunning, Run: longRunning}
	s := New(config.ServiceConfig{Name: "svc", Workdir: t.TempDir()}, config.Defaults{}, preset, "")
	go s.Start()
	waitFor(t, "first build", 10*time.Second, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.step != nil })
	first := func() *execution { s.mu.Lock(); defer s.mu.Unlock(); return s.step }()

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	s.preset.Build = ""
	err := s.Start()
	defer s.Stop()

	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if !waitProcessGone(uint32(first.pid()), 5*time.Second) {
		t.Errorf("first build PID %d survived the stop and would launch a duplicate", first.pid())
	}
	if got := s.Status(); got != StatusRunning {
		t.Errorf("Status() = %q, want %q", got, StatusRunning)
	}
}

func TestWatchRun_StaleExitDoesNotClobberCurrentRun(t *testing.T) {
	s := New(config.ServiceConfig{Name: "svc", Workdir: t.TempDir()}, config.Defaults{}, config.Preset{Run: longRunning}, "")
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stale := func() *execution { s.mu.Lock(); defer s.mu.Unlock(); return s.run }()
	parts, _ := shellSplit(longRunning)
	current, err := s.launch(exec.Command(parts[0], parts[1:]...))
	if err != nil {
		t.Fatalf("launch() error = %v", err)
	}
	s.mu.Lock()
	s.run = current
	s.pid = current.pid()
	s.mu.Unlock()
	go func() { _ = current.cmd.Wait(); current.finish() }()
	defer current.kill()

	stale.kill()
	<-stale.done

	if got := s.Status(); got != StatusRunning {
		t.Errorf("Status() = %q, want %q: a stale process exit overwrote the current one", got, StatusRunning)
	}
	s.mu.Lock()
	tracked := s.run
	s.mu.Unlock()
	if tracked != current {
		t.Error("stale watcher cleared the tracking of the current run process")
	}
}

func TestExecutionFinish_KillsDescendantWhoseParentExited(t *testing.T) {
	s := New(config.ServiceConfig{Name: "svc"}, config.Defaults{}, config.Preset{}, "")
	cmd := exec.Command("cmd", "/c", "start", "/b", "ping", "-n", "30", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	ex, err := s.launch(cmd)
	if err != nil {
		t.Fatalf("launch() error = %v", err)
	}
	if ex.job == nil {
		t.Fatal("process was not attached to a job object")
	}
	_ = cmd.Wait()
	var orphans []uint32
	waitFor(t, "detached ping in the job", 5*time.Second, func() bool {
		orphans = jobProcessIDs(t, ex.job)
		return len(orphans) > 0
	})

	ex.finish()

	for _, pid := range orphans {
		if !waitProcessGone(pid, 5*time.Second) {
			t.Errorf("descendant PID %d survived its parent's exit (taskkill /T cannot reach it)", pid)
		}
	}
}
