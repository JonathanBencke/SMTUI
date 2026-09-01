package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

type Manager struct {
	mu       sync.Mutex
	services []*Service
}

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{}
	for _, sc := range cfg.Services {
		m.services = append(m.services, newFromConfig(cfg, sc))
	}
	return m
}

func newFromConfig(cfg *config.Config, sc config.ServiceConfig) *Service {
	var preset config.Preset
	if sc.Runner != "" {
		if p, ok := cfg.Presets[sc.Runner]; ok {
			preset = p
		}
	}
	return New(sc, cfg.Defaults, preset, "")
}

// Reload rebuilds the manager's service list from cfg, reusing existing
// *Service instances by name instead of recreating everything from scratch.
// A full replace would discard the runtime state (pid, status, logs,
// startedAt, workdir override) of any service the caller has already
// started, orphaning its OS process and making the TUI lose track of it. A
// service whose name is no longer present in cfg is stopped (if active)
// before being dropped; a new name gets a fresh *Service.
func (m *Manager) Reload(cfg *config.Config) {
	m.mu.Lock()
	old := make(map[string]*Service, len(m.services))
	for _, s := range m.services {
		old[s.Name()] = s
	}

	kept := make(map[string]bool, len(cfg.Services))
	services := make([]*Service, 0, len(cfg.Services))
	for _, sc := range cfg.Services {
		if existing, ok := old[sc.Name]; ok {
			services = append(services, existing)
			kept[sc.Name] = true
			continue
		}
		services = append(services, newFromConfig(cfg, sc))
	}
	m.services = services

	var toStop []*Service
	for name, s := range old {
		if !kept[name] {
			toStop = append(toStop, s)
		}
	}
	m.mu.Unlock()

	for _, s := range toStop {
		if isActive(s.Status()) {
			go s.Stop()
		}
	}
}

func (m *Manager) Services() []*Service {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.services
}

func (m *Manager) ServiceByName(name string) *Service {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.services {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func (m *Manager) StartAll() {
	for _, s := range m.Services() {
		go s.Start()
	}
}

// StartAllConfigured starts every service from its configured workdir,
// dropping any custom directory set through Service.StartAt. It backs the
// terminal UI's "start all", which must always obey the configuration.
func (m *Manager) StartAllConfigured() {
	for _, s := range m.Services() {
		go s.StartConfigured()
	}
}

func (m *Manager) StopAll() {
	for _, s := range m.Services() {
		go s.Stop()
	}
}

// StopAllSync para todos os servicos em paralelo e espera ate timeout pelo
// termino. Usado no shutdown da TUI para evitar processos orphanos.
func (m *Manager) StopAllSync(timeout time.Duration) {
	var wg sync.WaitGroup
	for _, s := range m.Services() {
		if !isActive(s.Status()) {
			continue
		}
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			s.Stop()
		}(s)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// RunningCount retorna quantos servicos estao ativos (running/building/
// generating/stopping).
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.services {
		if isActive(s.Status()) {
			n++
		}
	}
	return n
}

func (m *Manager) SetCallbacks(onLog func(name, line string), onStatus func(name string, status Status, pid int)) {
	for _, s := range m.Services() {
		s.SetCallbacks(onLog, onStatus)
	}
}

func (m *Manager) SetProfilesForAll(profiles []string) {
	for _, s := range m.Services() {
		s.SetProfiles(profiles)
	}
}

func (m *Manager) Summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := 0
	for _, s := range m.services {
		if s.Status() == StatusRunning {
			running++
		}
	}
	return fmt.Sprintf("%d/%d running", running, len(m.services))
}
