// Package webconfig serves a local web page for editing the Service Manager
// configuration (defaults/tenant env, presets and services) and persisting it
// back to services.toml. It is launched on demand from the TUI (key `c`).
package webconfig

import (
	"context"
	"encoding/json"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

// DefaultPort is the loopback port the config web server listens on. It is
// distinct from the MCP port (9423).
const DefaultPort = 9424

//go:embed page.html
var pageHTML []byte

// Server is the local configuration web server.
type Server struct {
	cfgPath string
	port    int

	// OnSaved, if set, is invoked after the configuration is successfully
	// persisted (used by the TUI to reload).
	OnSaved func()

	mu         sync.Mutex
	httpServer *http.Server
	running    bool
	onLog      func(string)
}

// New creates a config web server for the given services.toml path.
func New(cfgPath string) *Server {
	return &Server{cfgPath: cfgPath, port: DefaultPort}
}

// SetLogCallback registers a sink for informational log lines.
func (s *Server) SetLogCallback(onLog func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLog = onLog
}

func (s *Server) emitLog(line string) {
	s.mu.Lock()
	cb := s.onLog
	s.mu.Unlock()
	if cb != nil {
		cb(line)
	}
}

// IsRunning reports whether the server is currently serving.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Port returns the configured port.
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// URL returns the base URL of the config page.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", s.Port())
}

// Start binds the loopback port and begins serving. It is a no-op error if
// already running.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("port %d: %w", s.port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.localhostOnly(s.handleIndex))
	mux.HandleFunc("/api/config", s.localhostOnly(s.handleConfig))

	httpServer := &http.Server{Handler: mux}

	s.mu.Lock()
	s.httpServer = httpServer
	s.running = true
	s.mu.Unlock()

	s.emitLog(fmt.Sprintf("Config web server started on %s", s.URL()))

	go func() {
		if err := httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.emitLog(fmt.Sprintf("Config web server error: %v", err))
		}
	}()

	return nil
}

// Stop gracefully shuts the server down and frees the port.
func (s *Server) Stop() error {
	s.mu.Lock()
	httpServer := s.httpServer
	s.running = false
	s.httpServer = nil
	s.mu.Unlock()

	if httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := httpServer.Shutdown(ctx)
	httpServer.Close()
	return err
}

// localhostOnly rejects requests whose Host header is not a loopback address,
// mitigating DNS-rebinding against this local-only editor.
func (s *Server) localhostOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden: config editor is localhost-only", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func isLoopbackHost(host string) bool {
	h := host
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.HasSuffix(host, "]") {
		h = host[:idx]
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(pageHTML)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getConfig(w, r)
	case http.MethodPost:
		s.saveConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadRaw(s.cfgPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var cfg config.Config
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid config JSON: %v", err))
		return
	}

	if err := config.SaveConfig(s.cfgPath, &cfg); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.emitLog(fmt.Sprintf("Configuration saved (%d services)", len(cfg.Services)))
	if s.OnSaved != nil {
		s.OnSaved()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": len(cfg.Services)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
