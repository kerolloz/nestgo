package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kerolloz/nestgo/internal/logger"
)

// TsConfig holds the compiler options we need from tsconfig.json.
type TsConfig struct {
	OutDir                 string              // compilerOptions.outDir (default "dist")
	RootDir                string              // compilerOptions.rootDir
	Paths                  map[string][]string // compilerOptions.paths
	TsBuildInfoFile        string              // compilerOptions.tsBuildInfoFile
	EmitDecoratorMetadata  bool
	ExperimentalDecorators bool
}

type tsConfigFile struct {
	Extends         string         `json:"extends"`
	CompilerOptions tsCompilerOpts `json:"compilerOptions"`
}

type tsCompilerOpts struct {
	OutDir                 string              `json:"outDir"`
	RootDir                string              `json:"rootDir"`
	Paths                  map[string][]string `json:"paths"`
	TsBuildInfoFile        string              `json:"tsBuildInfoFile"`
	EmitDecoratorMetadata  *bool               `json:"emitDecoratorMetadata"`
	ExperimentalDecorators *bool               `json:"experimentalDecorators"`
}

// LoadTsConfig reads and parses tsconfig resolving the extends chain.
func LoadTsConfig(cwd, tsConfigPath string) (*TsConfig, error) {
	absPath := tsConfigPath
	if !filepath.IsAbs(tsConfigPath) {
		absPath = filepath.Join(cwd, tsConfigPath)
	}
	return loadTsConfigAbs(cwd, absPath, 0)
}

func loadTsConfigAbs(cwd, absPath string, depth int) (*TsConfig, error) {
	if depth > 10 {
		logger.Warn("tsconfig extends chain exceeded depth limit (10). Parent configs beyond this depth are ignored: %s", absPath)
		return &TsConfig{OutDir: "dist"}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if depth == 0 {
			return nil, fmt.Errorf("could not read tsconfig at %s: %w", absPath, err)
		}
		return &TsConfig{OutDir: "dist"}, nil
	}

	stripped, stripErr := stripJSONC(data)
	if stripErr != nil {
		if depth == 0 {
			return nil, fmt.Errorf("failed to strip JSONC at %s: %w", absPath, stripErr)
		}
		return &TsConfig{OutDir: "dist"}, nil
	}

	var f tsConfigFile
	if err := json.Unmarshal(stripped, &f); err != nil {
		if depth == 0 {
			return nil, fmt.Errorf("failed to parse tsconfig at %s: %w", absPath, err)
		}
		return &TsConfig{OutDir: "dist"}, nil
	}

	result := &TsConfig{OutDir: "dist"}
	if f.Extends != "" {
		parentPath := resolveExtends(cwd, filepath.Dir(absPath), f.Extends)
		if parentPath != "" {
			parent, err := loadTsConfigAbs(cwd, parentPath, depth+1)
			if err == nil {
				result = parent
			}
		}
	}

	opts := f.CompilerOptions
	if opts.OutDir != "" {
		result.OutDir = filepath.Clean(opts.OutDir)
	}
	if opts.RootDir != "" {
		result.RootDir = opts.RootDir
	}
	if opts.Paths != nil {
		result.Paths = opts.Paths
	}
	if opts.TsBuildInfoFile != "" {
		result.TsBuildInfoFile = opts.TsBuildInfoFile
	}
	// These are boolean, override if explicitly set in child
	if opts.EmitDecoratorMetadata != nil {
		result.EmitDecoratorMetadata = *opts.EmitDecoratorMetadata
	}
	if opts.ExperimentalDecorators != nil {
		result.ExperimentalDecorators = *opts.ExperimentalDecorators
	}

	return result, nil
}

func resolveExtends(cwd, currentDir, extends string) string {
	// Relative path
	if extends[0] == '.' {
		p := filepath.Join(currentDir, extends)
		if filepath.Ext(p) == "" {
			p += ".json"
		}
		return p
	}

	// node_modules resolution
	p := filepath.Join(cwd, "node_modules", extends)
	if filepath.Ext(p) == "" {
		if _, err := os.Stat(p + ".json"); err == nil {
			return p + ".json"
		}
		p = filepath.Join(p, "tsconfig.json")
	}
	return p
}

func stripJSONC(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	i := 0
	inString := false
	for i < len(data) {
		c := data[i]
		if inString {
			result = append(result, c)
			if c == '\\' && i+1 < len(data) {
				i++
				result = append(result, data[i])
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			result = append(result, c)
			i++
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '*' {
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			if i+1 < len(data) {
				i += 2
			} else {
				return nil, fmt.Errorf("unterminated block comment in JSONC")
			}
			continue
		}
		result = append(result, c)
		i++
	}

	// Pass 2: strip trailing commas
	finalResult := make([]byte, 0, len(result))
	i = 0
	inString = false
	for i < len(result) {
		c := result[i]
		if inString {
			finalResult = append(finalResult, c)
			if c == '\\' && i+1 < len(result) {
				i++
				finalResult = append(finalResult, result[i])
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			finalResult = append(finalResult, c)
			i++
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(result) && (result[j] == ' ' || result[j] == '\n' || result[j] == '\r' || result[j] == '\t') {
				j++
			}
			if j < len(result) && (result[j] == '}' || result[j] == ']') {
				i++
				continue
			}
		}
		finalResult = append(finalResult, c)
		i++
	}

	return finalResult, nil
}
