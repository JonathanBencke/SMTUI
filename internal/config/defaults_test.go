package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertDefaultsEnvReplacesSectionPreservingRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")
	original := `# top comment
[presets.node-npm]
run = "npm run dev"

[defaults]
[defaults.env]
TENANT = "old-tenant"
BROKER_PORT = "5674"

[[service]]
name = "Web"
runner = "node-npm"
workdir = "."
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	err := UpsertDefaultsEnv(path, map[string]string{
		"TENANT":       "acme",
		"VIRTUAL_HOST": "acme",
	})
	if err != nil {
		t.Fatalf("UpsertDefaultsEnv() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Defaults.Env["TENANT"] != "acme" {
		t.Errorf("TENANT = %q, want acme", cfg.Defaults.Env["TENANT"])
	}
	if cfg.Defaults.Env["VIRTUAL_HOST"] != "acme" {
		t.Errorf("VIRTUAL_HOST = %q, want acme", cfg.Defaults.Env["VIRTUAL_HOST"])
	}
	// Untouched existing key must be preserved.
	if cfg.Defaults.Env["BROKER_PORT"] != "5674" {
		t.Errorf("BROKER_PORT = %q, want 5674 (should be preserved)", cfg.Defaults.Env["BROKER_PORT"])
	}
	// Presets, services and comments must survive.
	if _, ok := cfg.Presets["node-npm"]; !ok {
		t.Error("preset node-npm was lost")
	}
	if len(cfg.Services) != 1 {
		t.Errorf("len(Services) = %d, want 1", len(cfg.Services))
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# top comment") {
		t.Error("top comment was lost")
	}
}

func TestUpsertDefaultsEnvCreatesSectionWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")
	original := "[presets.node-npm]\nrun = \"npm run dev\"\n\n[[service]]\nname = \"Web\"\nrunner = \"node-npm\"\nworkdir = \".\"\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := UpsertDefaultsEnv(path, map[string]string{"TENANT": "acme"}); err != nil {
		t.Fatalf("UpsertDefaultsEnv() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Defaults.Env["TENANT"] != "acme" {
		t.Errorf("TENANT = %q, want acme", cfg.Defaults.Env["TENANT"])
	}
	if _, ok := cfg.Presets["node-npm"]; !ok {
		t.Error("preset node-npm was lost")
	}
}
