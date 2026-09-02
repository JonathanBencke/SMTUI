package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/BurntSushi/toml"
)

// ErrNoConfigFile is returned by Load when the config file does not exist.
// It lets callers distinguish a missing file (first run) from a present but
// empty configuration (ErrNoServices).
var ErrNoConfigFile = errors.New("config file not found")

// ErrNoServices is returned by Load when the config file exists and parses
// correctly but defines no [[service]] entries.
var ErrNoServices = errors.New("no services defined")

// Defaults holds global settings applied to every service.
type Defaults struct {
	// Env is a free-form map of environment variables injected into every
	// service process. Values support Go templates (see service.templateData).
	Env map[string]string `toml:"env,omitempty" json:"env,omitempty"`
}

// Preset is a reusable definition of how to build and run a service.
// Both Build and Run are Go template strings expanded with the service's
// template data. Env is merged on top of Defaults.Env.
type Preset struct {
	Build string            `toml:"build,omitempty" json:"build,omitempty"`
	Run   string            `toml:"run,omitempty" json:"run,omitempty"`
	Env   map[string]string `toml:"env,omitempty" json:"env,omitempty"`
	// SdlGenerateCommand overrides the default "mvn clean generate-sources"
	// command used by the on-demand source generation (the `r` key in the TUI
	// or the generate_sources MCP tool) for services whose workdir sits inside
	// a Senior SDL/PDL/EDL project (detected via a *.sdl file in a parent
	// directory). It is never run as part of starting a service. Leave empty
	// to use the default.
	SdlGenerateCommand string `toml:"sdl_generate_command,omitempty" json:"sdl_generate_command,omitempty"`
	// GenerateInWorkdir allows SdlGenerateCommand to run in the service workdir
	// without requiring an SDL/PDL/EDL marker. Use it only for projects that
	// expose an explicit, standalone source-generation command.
	GenerateInWorkdir bool `toml:"generate_in_workdir,omitempty" json:"generate_in_workdir,omitempty"`
}

// templateActionRe matches Go template actions like {{.Modules}} or
// {{if .Profiles}}. templateVarRe extracts the leading field identifier
// (e.g. Modules, MainClass) from within an action.
var (
	templateActionRe = regexp.MustCompile(`{{[^}]*}}`)
	templateVarRe    = regexp.MustCompile(`\.([A-Z][A-Za-z0-9_]*)`)
)

// ambientTemplateVars are template variables that are supplied by the runtime
// (not by the user) and therefore must not be prompted for in the wizard.
var ambientTemplateVars = map[string]bool{
	"Name":    true,
	"Workdir": true,
	"Path":    true,
}

// TemplateVars scans the preset's Build, Run and Env template strings and
// returns the sorted, de-duplicated list of user-supplied template variables
// referenced (e.g. [JavaHome, MainClass, Modules, Profiles]). Ambient
// variables (Name, Workdir, Path) are excluded because they are not filled in
// by the user through the wizard.
func (p Preset) TemplateVars() []string {
	sources := make([]string, 0, len(p.Env)+2)
	sources = append(sources, p.Build, p.Run)
	for _, v := range p.Env {
		sources = append(sources, v)
	}

	seen := make(map[string]bool)
	for _, src := range sources {
		for _, action := range templateActionRe.FindAllString(src, -1) {
			for _, match := range templateVarRe.FindAllStringSubmatch(action, -1) {
				name := match[1]
				if ambientTemplateVars[name] {
					continue
				}
				seen[name] = true
			}
		}
	}

	vars := make([]string, 0, len(seen))
	for name := range seen {
		vars = append(vars, name)
	}
	sort.Strings(vars)
	return vars
}

// ServiceConfig describes a single managed process.
type ServiceConfig struct {
	Name string `toml:"name" json:"name"`
	// Workdir is the working directory for build/run commands. Relative paths
	// are resolved against the config file location.
	Workdir string `toml:"workdir" json:"workdir"`
	// Runner references a [presets.<name>] entry. Optional.
	Runner string `toml:"runner,omitempty" json:"runner,omitempty"`
	// BuildCommand overrides the preset's build command. Optional.
	BuildCommand string `toml:"build_command,omitempty" json:"build_command,omitempty"`
	// RunCommand overrides the preset's run command. Optional.
	RunCommand string `toml:"run_command,omitempty" json:"run_command,omitempty"`
	// Env is a free-form map of service-specific environment variables merged
	// on top of defaults and preset env.
	Env map[string]string `toml:"env,omitempty" json:"env,omitempty"`

	// Vars holds custom preset template variables (e.g. IntegrationProperties)
	// that don't map to a dedicated field. They are exposed to build/run/env
	// templates by their key, so a preset referencing {{.IntegrationProperties}}
	// reads it from here. Written as [service.vars] in the config file.
	Vars map[string]string `toml:"vars,omitempty" json:"vars,omitempty"`

	// The fields below are not interpreted directly; they are exposed to the
	// build/run/env templates as variables (e.g. {{.JavaHome}}, {{.Modules}}).
	JavaHome   string   `toml:"java_home,omitempty" json:"java_home,omitempty"`
	Modules    []string `toml:"modules,omitempty" json:"modules,omitempty"`
	MainClass  string   `toml:"main_class,omitempty" json:"main_class,omitempty"`
	Profiles   []string `toml:"profiles,omitempty" json:"profiles,omitempty"`
	HealthPort int      `toml:"health_port,omitempty" json:"health_port,omitempty"`
}

// Config is the parsed configuration file.
type Config struct {
	Defaults Defaults          `toml:"defaults" json:"defaults"`
	Presets  map[string]Preset `toml:"presets" json:"presets"`
	Services []ServiceConfig   `toml:"service" json:"services"`
}

// Exists reports whether a config file is present at path.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Load reads and parses the TOML config at path.
//
// It returns ErrNoConfigFile if the file does not exist and ErrNoServices if
// the file parses but defines no services, so callers can react differently
// (e.g. generate a starter file or open the onboarding screen).
func Load(path string) (*Config, error) {
	cfg, err := LoadAllowEmpty(path)
	if err != nil {
		return nil, err
	}
	if len(cfg.Services) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrNoServices)
	}
	return cfg, nil
}

// LoadAllowEmpty is like Load but tolerates a configuration with no services.
// It is used by the onboarding flow, which needs the presets available even
// before the user has created any service.
func LoadAllowEmpty(path string) (*Config, error) {
	if !Exists(path) {
		return nil, fmt.Errorf("%s: %w", path, ErrNoConfigFile)
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	for i := range cfg.Services {
		if filepath.IsAbs(cfg.Services[i].Workdir) {
			continue
		}
		cfg.Services[i].Workdir = filepath.Join(abs, cfg.Services[i].Workdir)
	}
	return &cfg, nil
}

// DefaultPath returns the default config path: a services.toml next to the
// executable if present, otherwise ./services.toml in the working directory.
func DefaultPath() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "services.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "services.toml"
}

// LoadRaw decodes the config without resolving service workdirs to absolute
// paths. It is used by the web config editor, which must round-trip the file
// as authored (relative workdirs preserved). Returns an empty Config if the
// file does not exist yet.
func LoadRaw(path string) (*Config, error) {
	cfg := &Config{Presets: map[string]Preset{}}
	if !Exists(path) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if cfg.Presets == nil {
		cfg.Presets = map[string]Preset{}
	}
	return cfg, nil
}

// SaveConfig serialises cfg back to TOML at path. Note: comments in the
// original file are not preserved — the config editor becomes the source of
// truth. The file is written atomically via a temp file + rename.
func SaveConfig(path string, cfg *Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".services-*.toml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
