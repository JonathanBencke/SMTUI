package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
)

func TestResolveStartupMissingFileGeneratesStarter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")

	info, err := resolveStartup(path)
	if err != nil {
		t.Fatalf("resolveStartup() error = %v", err)
	}

	if !config.Exists(path) {
		t.Error("resolveStartup() did not create a starter config")
	}
	if !info.onboarding {
		t.Error("onboarding = false, want true for a freshly generated starter")
	}
	if _, ok := info.cfg.Presets["java-maven"]; !ok {
		t.Error("generated starter is missing the java-maven preset")
	}
}

func TestResolveStartupExistingNoServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")
	original := "[presets.node-npm]\nrun = \"npm run dev\"\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := resolveStartup(path)
	if err != nil {
		t.Fatalf("resolveStartup() error = %v", err)
	}

	if !info.onboarding {
		t.Error("onboarding = false, want true when no services are defined")
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Error("resolveStartup() overwrote an existing config file")
	}
}

func TestResolveStartupExistingWithServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")
	content := "[presets.node-npm]\nrun = \"npm run dev\"\n\n[[service]]\nname = \"Web\"\nrunner = \"node-npm\"\nworkdir = \".\"\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := resolveStartup(path)
	if err != nil {
		t.Fatalf("resolveStartup() error = %v", err)
	}

	if info.onboarding {
		t.Error("onboarding = true, want false when services exist")
	}
	if len(info.cfg.Services) != 1 {
		t.Errorf("len(Services) = %d, want 1", len(info.cfg.Services))
	}
}
