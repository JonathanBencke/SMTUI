package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPresetTemplateVars(t *testing.T) {
	tests := []struct {
		name   string
		preset Preset
		want   []string
	}{
		{
			name: "java-maven",
			preset: Preset{
				Build: "mvn -pl {{.Modules}} -am install -DskipTests",
				Run:   "mvn -pl impl compile exec:java -Dexec.mainClass={{.MainClass}}{{if .Profiles}} -Dexec.args=--spring.profiles.active={{.Profiles}}{{end}}",
				Env: map[string]string{
					"JAVA_HOME": "{{.JavaHome}}",
					"PATH":      `{{.JavaHome}}\bin;{{.Path}}`,
				},
			},
			want: []string{"JavaHome", "MainClass", "Modules", "Profiles"},
		},
		{
			name: "wildfly",
			preset: Preset{
				Run: `cmd /c "{{.MainClass}}"`,
				Env: map[string]string{"JAVA_HOME": "{{.JavaHome}}"},
			},
			want: []string{"JavaHome", "MainClass"},
		},
		{
			name:   "node-npm has no user vars",
			preset: Preset{Run: "npm run dev"},
			want:   []string{},
		},
		{
			name: "ambient vars are excluded",
			preset: Preset{
				Run: "run {{.Name}} in {{.Workdir}} with {{.Path}}",
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.preset.TemplateVars()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TemplateVars() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPresetSdlGenerateCommandRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		preset Preset
		want   string
	}{
		{
			name:   "custom command is preserved",
			preset: Preset{Run: "mvn exec:java", SdlGenerateCommand: "mvn -pl impl clean generate-sources"},
			want:   "mvn -pl impl clean generate-sources",
		},
		{
			name:   "empty command stays empty (omitempty)",
			preset: Preset{Run: "npm start"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "services.toml")
			cfg := &Config{
				Presets: map[string]Preset{"p": tt.preset},
				Services: []ServiceConfig{
					{Name: "svc", Workdir: ".", Runner: "p"},
				},
			}

			if err := SaveConfig(path, cfg); err != nil {
				t.Fatalf("SaveConfig() error = %v", err)
			}

			loaded, err := LoadRaw(path)
			if err != nil {
				t.Fatalf("LoadRaw() error = %v", err)
			}

			got := loaded.Presets["p"].SdlGenerateCommand
			if got != tt.want {
				t.Errorf("SdlGenerateCommand = %q, want %q", got, tt.want)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			hasField := strings.Contains(string(raw), "sdl_generate_command")
			wantField := tt.want != ""
			if hasField != wantField {
				t.Errorf("sdl_generate_command present in file = %v, want %v (omitempty)", hasField, wantField)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.toml")

	_, err := Load(path)

	if !errors.Is(err, ErrNoConfigFile) {
		t.Fatalf("Load() error = %v, want ErrNoConfigFile", err)
	}
}

func TestLoadNoServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.toml")
	if err := os.WriteFile(path, []byte("[presets.java-maven]\nrun = \"x\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)

	if !errors.Is(err, ErrNoServices) {
		t.Fatalf("Load() error = %v, want ErrNoServices", err)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "services.toml")
	if err := os.WriteFile(present, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if !Exists(present) {
		t.Error("Exists() = false for present file, want true")
	}
	if Exists(filepath.Join(dir, "missing.toml")) {
		t.Error("Exists() = true for missing file, want false")
	}
	if Exists(dir) {
		t.Error("Exists() = true for directory, want false")
	}
}

func TestWriteStarter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")

	if err := WriteStarter(path); err != nil {
		t.Fatalf("WriteStarter() error = %v", err)
	}

	// The starter has all services commented out, so Load reports ErrNoServices
	// (proving it parses cleanly but defines no services yet).
	if _, err := Load(path); !errors.Is(err, ErrNoServices) {
		t.Fatalf("Load(starter) error = %v, want ErrNoServices", err)
	}

	// Append a minimal service and confirm the shipped presets parse.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[[service]]\nname = \"X\"\nrunner = \"node-npm\"\nworkdir = \".\"\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after appending service error = %v", err)
	}
	for _, name := range []string{"java-maven", "wildfly", "hcm-integration", "node-npm", "spring-boot"} {
		if _, ok := cfg.Presets[name]; !ok {
			t.Errorf("starter missing preset %q", name)
		}
	}
	if b := cfg.Presets["hcm-integration"].Build; !strings.Contains(b, "{{.IntegrationPropertiesDir}}") {
		t.Errorf("hcm-integration build should reference {{.IntegrationPropertiesDir}}, got: %s", b)
	}
}

func TestWriteStarterDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.toml")
	original := []byte("# my config\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteStarter(path); err != nil {
		t.Fatalf("WriteStarter() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Error("WriteStarter() overwrote an existing file")
	}
}
