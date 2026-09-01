package webconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

func TestGetConfigReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.toml")
	os.WriteFile(path, []byte("[presets.node-npm]\nrun = \"npm run dev\"\n\n[[service]]\nname = \"Web\"\nrunner = \"node-npm\"\nworkdir = \".\"\n"), 0644)

	s := New(path)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Host = "127.0.0.1:9424"

	s.localhostOnly(s.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var cfg config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := cfg.Presets["node-npm"]; !ok {
		t.Error("preset node-npm missing from response")
	}
	if len(cfg.Services) != 1 || cfg.Services[0].Name != "Web" {
		t.Errorf("services = %+v", cfg.Services)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.toml")

	s := New(path)
	saved := false
	s.OnSaved = func() { saved = true }

	body := `{"defaults":{"env":{"TENANT":"acme"}},"presets":{"node-npm":{"run":"npm run dev"}},"services":[{"name":"Web","workdir":".","runner":"node-npm"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	req.Host = "localhost:9424"

	s.localhostOnly(s.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !saved {
		t.Error("OnSaved was not invoked")
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("reloading saved config: %v", err)
	}
	if cfg.Defaults.Env["TENANT"] != "acme" {
		t.Errorf("TENANT = %q, want acme", cfg.Defaults.Env["TENANT"])
	}
	if cfg.Services[0].Name != "Web" {
		t.Errorf("service name = %q", cfg.Services[0].Name)
	}
	if _, ok := cfg.Presets["node-npm"]; !ok {
		t.Error("preset node-npm not persisted")
	}
}

func TestSaveConfigRoundTrip_SdlGenerateCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.toml")

	s := New(path)

	body := `{"defaults":{},"presets":{"java-maven":{"build":"mvn install","run":"mvn exec:java","sdl_generate_command":"mvn -pl impl clean generate-sources"}},"services":[{"name":"Api","workdir":".","runner":"java-maven"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	req.Host = "127.0.0.1:9424"

	s.localhostOnly(s.handleConfig)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("reloading saved config: %v", err)
	}
	got := cfg.Presets["java-maven"].SdlGenerateCommand
	want := "mvn -pl impl clean generate-sources"
	if got != want {
		t.Errorf("SdlGenerateCommand = %q, want %q", got, want)
	}
}

func TestRejectsNonLoopbackHost(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "services.toml"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Host = "evil.example.com"

	s.localhostOnly(s.handleConfig)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-loopback Host", rec.Code)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:9424": true,
		"localhost:9424": true,
		"[::1]:9424":     true,
		"127.0.0.1":      true,
		"evil.com:9424":  false,
		"10.0.0.5:9424":  false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
