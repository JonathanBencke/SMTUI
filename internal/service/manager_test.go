package service

import (
	"testing"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

func testManager(t *testing.T, names ...string) *Manager {
	t.Helper()
	cfg := &config.Config{}
	for _, n := range names {
		cfg.Services = append(cfg.Services, config.ServiceConfig{Name: n, Workdir: t.TempDir()})
	}
	return NewManager(cfg)
}

func TestRunningCount_CountsGeneratingAsActive(t *testing.T) {
	m := testManager(t, "a", "b")

	if got := m.RunningCount(); got != 0 {
		t.Fatalf("RunningCount() = %d, want 0", got)
	}

	m.ServiceByName("a").setStatus(StatusGenerating, 0)

	if got := m.RunningCount(); got != 1 {
		t.Errorf("RunningCount() = %d, want 1 (generating is active)", got)
	}
}

func TestReload_PreservesRunningServiceInstance(t *testing.T) {
	m := testManager(t, "a", "b")
	svc := m.ServiceByName("a")
	svc.setStatus(StatusRunning, 4242)

	cfg := &config.Config{Services: []config.ServiceConfig{
		{Name: "a", Workdir: t.TempDir()},
		{Name: "b", Workdir: t.TempDir()},
	}}
	m.Reload(cfg)

	got := m.ServiceByName("a")
	if got != svc {
		t.Fatal("Reload() replaced the *Service instance for an unchanged service, losing its runtime state")
	}
	if got.Status() != StatusRunning || got.PID() != 4242 {
		t.Errorf("Reload() reset status/pid: got status=%v pid=%d, want StatusRunning/4242", got.Status(), got.PID())
	}
}

func TestReload_AddsNewServiceWithoutTouchingExisting(t *testing.T) {
	m := testManager(t, "a")
	svc := m.ServiceByName("a")
	svc.setStatus(StatusRunning, 111)

	cfg := &config.Config{Services: []config.ServiceConfig{
		{Name: "a", Workdir: t.TempDir()},
		{Name: "b", Workdir: t.TempDir()},
	}}
	m.Reload(cfg)

	if got := m.ServiceByName("a"); got != svc || got.Status() != StatusRunning {
		t.Fatal("Reload() disturbed the existing service when only adding a new one")
	}
	b := m.ServiceByName("b")
	if b == nil {
		t.Fatal("Reload() did not add the new service")
	}
	if b.Status() != StatusIdle {
		t.Errorf("new service status = %v, want StatusIdle", b.Status())
	}
	if len(m.Services()) != 2 {
		t.Errorf("Services() len = %d, want 2", len(m.Services()))
	}
}

func TestReload_StopsRemovedActiveService(t *testing.T) {
	m := testManager(t, "a", "b")
	m.ServiceByName("b").setStatus(StatusRunning, 0)

	cfg := &config.Config{Services: []config.ServiceConfig{
		{Name: "a", Workdir: t.TempDir()},
	}}
	m.Reload(cfg)

	if got := m.ServiceByName("b"); got != nil {
		t.Fatal("Reload() kept a service that was removed from config")
	}
	if len(m.Services()) != 1 {
		t.Errorf("Services() len = %d, want 1", len(m.Services()))
	}
}

func TestStopAllSync_ReturnsWithGeneratingService(t *testing.T) {
	m := testManager(t, "a")
	m.ServiceByName("a").setStatus(StatusGenerating, 0)

	done := make(chan struct{})
	go func() {
		m.StopAllSync(2 * time.Second)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAllSync() did not return for a generating service")
	}
}
