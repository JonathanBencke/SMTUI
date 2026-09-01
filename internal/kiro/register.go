// Package kiro handles integration with kiro-cli, notably registering this
// application as an MCP server in kiro-cli's settings.
package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultServerName is the key used for this server in kiro-cli's mcp.json.
const DefaultServerName = "service-manager"

// ConfigPath returns the kiro-cli MCP settings file for the given scope.
// scope is "global" (~/.kiro/settings/mcp.json) or "workspace"
// (.kiro/settings/mcp.json relative to the current directory).
func ConfigPath(scope string) (string, error) {
	switch scope {
	case "", "global":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory: %w", err)
		}
		return filepath.Join(home, ".kiro", "settings", "mcp.json"), nil
	case "workspace":
		return filepath.Join(".kiro", "settings", "mcp.json"), nil
	default:
		return "", fmt.Errorf("invalid scope %q (use \"global\" or \"workspace\")", scope)
	}
}

// mergeServer merges an MCP server entry into existing mcp.json content,
// preserving any other servers and top-level keys. If existing is empty a new
// document is created. The result is indented JSON.
func mergeServer(existing []byte, name string, entry map[string]any) ([]byte, error) {
	doc := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf("parsing existing mcp.json: %w", err)
		}
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	servers[name] = entry
	doc["mcpServers"] = servers

	return json.MarshalIndent(doc, "", "  ")
}

// removeServer removes the named server from existing mcp.json content. It
// returns the updated JSON and whether the server was present.
func removeServer(existing []byte, name string) ([]byte, bool, error) {
	if len(existing) == 0 {
		return existing, false, nil
	}
	doc := map[string]any{}
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, false, fmt.Errorf("parsing existing mcp.json: %w", err)
	}
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		return existing, false, nil
	}
	if _, present := servers[name]; !present {
		return existing, false, nil
	}
	delete(servers, name)
	doc["mcpServers"] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// InstallMCP registers the running TUI's SSE server as a remote MCP server
// named DefaultServerName in kiro-cli's settings for the given scope. kiro-cli
// then connects to the server already started by the TUI (no new process is
// spawned). It returns the settings path.
func InstallMCP(scope, url string) (string, error) {
	path, err := ConfigPath(scope)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("creating settings directory: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	merged, err := mergeServer(existing, DefaultServerName, map[string]any{"url": url})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, merged, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// UninstallMCP removes the DefaultServerName entry from kiro-cli's settings for
// the given scope. It returns the settings path and whether an entry was
// removed.
func UninstallMCP(scope string) (string, bool, error) {
	path, err := ConfigPath(scope)
	if err != nil {
		return "", false, err
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return "", false, err
	}
	updated, removed, err := removeServer(existing, DefaultServerName)
	if err != nil {
		return "", false, err
	}
	if removed {
		if err := os.WriteFile(path, updated, 0644); err != nil {
			return "", false, err
		}
	}
	return path, removed, nil
}
