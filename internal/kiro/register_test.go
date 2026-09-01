package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeServerCreatesDocument(t *testing.T) {
	out, err := mergeServer(nil, "service-manager", map[string]any{"url": "http://127.0.0.1:9423/sse"})
	if err != nil {
		t.Fatalf("mergeServer() error = %v", err)
	}

	var doc struct {
		McpServers map[string]struct {
			URL string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	srv, ok := doc.McpServers["service-manager"]
	if !ok {
		t.Fatal("service-manager entry not created")
	}
	if srv.URL != "http://127.0.0.1:9423/sse" {
		t.Errorf("url = %q", srv.URL)
	}
}

func TestMergeServerPreservesOthers(t *testing.T) {
	existing := []byte(`{"mcpServers":{"git":{"command":"mcp-server-git","args":["--stdio"]}},"otherKey":123}`)

	out, err := mergeServer(existing, "service-manager", map[string]any{"url": "http://127.0.0.1:9423/sse"})
	if err != nil {
		t.Fatalf("mergeServer() error = %v", err)
	}

	doc := map[string]any{}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["git"]; !ok {
		t.Error("existing 'git' server was lost")
	}
	if _, ok := servers["service-manager"]; !ok {
		t.Error("service-manager not added")
	}
	if _, ok := doc["otherKey"]; !ok {
		t.Error("unknown top-level key was lost")
	}
}

func TestRemoveServer(t *testing.T) {
	existing := []byte(`{"mcpServers":{"git":{"command":"x"},"service-manager":{"command":"smtui"}}}`)

	out, removed, err := removeServer(existing, "service-manager")
	if err != nil {
		t.Fatalf("removeServer() error = %v", err)
	}
	if !removed {
		t.Fatal("expected removed = true")
	}
	doc := map[string]any{}
	json.Unmarshal(out, &doc)
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["service-manager"]; ok {
		t.Error("service-manager was not removed")
	}
	if _, ok := servers["git"]; !ok {
		t.Error("git server should be preserved")
	}
}

func TestRemoveServerAbsent(t *testing.T) {
	_, removed, err := removeServer([]byte(`{"mcpServers":{}}`), "service-manager")
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Error("expected removed = false when server absent")
	}
}

func TestInstallAndUninstallWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	path, err := InstallMCP("workspace", "http://127.0.0.1:9423/sse")
	if err != nil {
		t.Fatalf("InstallMCP() error = %v", err)
	}
	if path != filepath.Join(".kiro", "settings", "mcp.json") {
		t.Errorf("unexpected path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file not written: %v", err)
	}

	_, removed, err := UninstallMCP("workspace")
	if err != nil {
		t.Fatalf("UninstallMCP() error = %v", err)
	}
	if !removed {
		t.Error("expected the server to be removed")
	}
}

func TestConfigPathInvalidScope(t *testing.T) {
	if _, err := ConfigPath("bogus"); err == nil {
		t.Error("expected error for invalid scope")
	}
}
