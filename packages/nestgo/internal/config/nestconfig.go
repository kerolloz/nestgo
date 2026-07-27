package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Asset mirrors the nest-cli Asset union type: either a plain string glob or
// an object with extra options.
type Asset struct {
	// Plain string form — set when the JSON value is a string.
	Glob string

	// Object form fields.
	Include     string `json:"include"`
	Exclude     string `json:"exclude"`
	OutDir      string `json:"outDir"`
	WatchAssets bool   `json:"watchAssets"`
}

func (a *Asset) UnmarshalJSON(data []byte) error {
	// Try as plain string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		a.Glob = s
		return nil
	}
	// Otherwise unmarshal as object.
	type assetObj struct {
		Include     string `json:"include"`
		Exclude     string `json:"exclude"`
		OutDir      string `json:"outDir"`
		WatchAssets bool   `json:"watchAssets"`
	}
	var obj assetObj
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	a.Include = obj.Include
	a.Exclude = obj.Exclude
	a.OutDir = obj.OutDir
	a.WatchAssets = obj.WatchAssets
	return nil
}

// ResolvedGlob returns the effective include glob, regardless of form.
func (a *Asset) ResolvedGlob() string {
	if a.Glob != "" {
		return a.Glob
	}
	return a.Include
}

// Plugin is a NestJS CLI plugin entry. The JSON form is either a bare package
// name or an object with options.
type Plugin struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options,omitempty"`
}

func (p *Plugin) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		p.Name = name
		return nil
	}
	type pluginObj struct {
		Name    string         `json:"name"`
		Options map[string]any `json:"options"`
	}
	var obj pluginObj
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	p.Name, p.Options = obj.Name, obj.Options
	return nil
}

// CompilerOptions holds the subset of compilerOptions we care about.
type CompilerOptions struct {
	TsConfigPath string   `json:"tsConfigPath"`
	DeleteOutDir bool     `json:"deleteOutDir"`
	WatchAssets  bool     `json:"watchAssets"`
	Assets       []Asset  `json:"assets"`
	Plugins      []Plugin `json:"plugins"`
	BuilderType  string   // resolved to "tsc", "swc", or "webpack"
}

type compilerOptionsRaw struct {
	TsConfigPath string          `json:"tsConfigPath"`
	DeleteOutDir *bool           `json:"deleteOutDir"`
	WatchAssets  *bool           `json:"watchAssets"`
	Assets       []Asset         `json:"assets"`
	Plugins      []Plugin        `json:"plugins"`
	Builder      json.RawMessage `json:"builder"`
}

// NestConfig holds the subset of nest-cli.json fields used by nestgo.
type NestConfig struct {
	SourceRoot      string          `json:"sourceRoot"`
	EntryFile       string          `json:"entryFile"`
	Exec            string          `json:"exec"`
	CompilerOptions CompilerOptions // parsed from compilerOptionsRaw
}

type nestConfigRaw struct {
	SourceRoot      string             `json:"sourceRoot"`
	EntryFile       string             `json:"entryFile"`
	Exec            string             `json:"exec"`
	CompilerOptions compilerOptionsRaw `json:"compilerOptions"`
}

// defaults mirrors nest-cli's defaultConfiguration.
var defaults = NestConfig{
	SourceRoot: "src",
	EntryFile:  "main",
	Exec:       "node",
	CompilerOptions: CompilerOptions{
		BuilderType: "tsc",
	},
}

// Load reads nest-cli.json (or .nest-cli.json) from cwd, merges with defaults,
// and validates. Pass configPath="" to auto-detect.
func Load(cwd, configPath string) (*NestConfig, error) {
	var data []byte
	var err error
	if configPath != "" {
		data, err = os.ReadFile(filepath.Join(cwd, configPath))
		if err != nil {
			return nil, fmt.Errorf("could not read config file %q: %w", configPath, err)
		}
	} else {
		for _, name := range []string{"nest-cli.json", ".nest-cli.json"} {
			p := filepath.Join(cwd, name)
			data, err = os.ReadFile(p)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		// No config found — use pure defaults.
		if data == nil {
			cfg := defaults
			return &cfg, nil
		}
	}

	var raw nestConfigRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid nest-cli.json: %w", err)
	}

	// Merge with defaults.
	cfg := defaults
	if raw.SourceRoot != "" {
		cfg.SourceRoot = raw.SourceRoot
	}
	if raw.EntryFile != "" {
		cfg.EntryFile = raw.EntryFile
	}
	if raw.Exec != "" {
		cfg.Exec = raw.Exec
	}
	if raw.CompilerOptions.TsConfigPath != "" {
		cfg.CompilerOptions.TsConfigPath = raw.CompilerOptions.TsConfigPath
	}
	if raw.CompilerOptions.DeleteOutDir != nil {
		cfg.CompilerOptions.DeleteOutDir = *raw.CompilerOptions.DeleteOutDir
	}
	if raw.CompilerOptions.WatchAssets != nil {
		cfg.CompilerOptions.WatchAssets = *raw.CompilerOptions.WatchAssets
	}
	cfg.CompilerOptions.Assets = raw.CompilerOptions.Assets
	cfg.CompilerOptions.Plugins = raw.CompilerOptions.Plugins

	// Resolve builder type.
	if raw.CompilerOptions.Builder != nil {
		builderType, err := resolveBuilderType(raw.CompilerOptions.Builder)
		if err != nil {
			return nil, err
		}
		cfg.CompilerOptions.BuilderType = builderType
	}

	// Validate: only tsc (default) is supported.
	if cfg.CompilerOptions.BuilderType != "" && cfg.CompilerOptions.BuilderType != "tsc" {
		return nil, fmt.Errorf(
			"nestgo only supports the 'tsc' builder, but nest-cli.json specifies %q.\n"+
				"webpack and swc builders are not supported by nestgo",
			cfg.CompilerOptions.BuilderType,
		)
	}

	return &cfg, nil
}

func resolveBuilderType(raw json.RawMessage) (string, error) {
	// Could be a plain string: "tsc"
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Or an object: {"type": "tsc", "options": {...}}
	var obj struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("invalid builder field in nest-cli.json: %w", err)
	}
	return obj.Type, nil
}
