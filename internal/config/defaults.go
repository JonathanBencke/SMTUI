package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// UpsertDefaultsEnv merges the given key/value pairs into the [defaults.env]
// section of the config file at path, preserving the rest of the file (presets,
// services and comments). Existing keys not present in updates are kept.
func UpsertDefaultsEnv(path string, updates map[string]string) error {
	existing := map[string]string{}
	if cfg, err := LoadAllowEmpty(path); err == nil && cfg.Defaults.Env != nil {
		for k, v := range cfg.Defaults.Env {
			existing[k] = v
		}
	}
	for k, v := range updates {
		existing[k] = v
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated := rewriteDefaultsEnv(string(content), existing)
	return os.WriteFile(path, []byte(updated), 0644)
}

// rewriteDefaultsEnv returns content with its [defaults.env] section replaced by
// the sorted key/value pairs in env. If the section (or the [defaults] table)
// does not exist, a fresh block is created.
func rewriteDefaultsEnv(content string, env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make([]string, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, fmt.Sprintf("%s = %q", k, env[k]))
	}

	lines := strings.Split(content, "\n")

	// Case 1: [defaults.env] already present — replace its body up to the next
	// table header.
	if hdr := indexOfHeader(lines, "[defaults.env]"); hdr >= 0 {
		end := len(lines)
		for j := hdr + 1; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
				end = j
				break
			}
		}
		out := append([]string{}, lines[:hdr+1]...)
		out = append(out, entries...)
		if end < len(lines) {
			out = append(out, "")
		}
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n")
	}

	// Case 2: [defaults] present without [defaults.env] — insert the subtable.
	if hdr := indexOfHeader(lines, "[defaults]"); hdr >= 0 {
		out := append([]string{}, lines[:hdr+1]...)
		out = append(out, "[defaults.env]")
		out = append(out, entries...)
		out = append(out, lines[hdr+1:]...)
		return strings.Join(out, "\n")
	}

	// Case 3: neither present — append a fresh block.
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	block := "\n[defaults]\n[defaults.env]\n" + strings.Join(entries, "\n") + "\n"
	return content + block
}

func indexOfHeader(lines []string, header string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			return i
		}
	}
	return -1
}
