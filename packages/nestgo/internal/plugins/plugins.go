// Package plugins generates NestJS CLI plugin metadata.
//
// @nestjs/swagger and @nestjs/graphql ship their CLI plugins as TypeScript
// custom transformers. TypeScript 7 has no transformer API and will not gain
// one — the compiler is a Go binary and cannot load JavaScript into its
// process — so those plugins cannot run under nestgo, nor under the Nest CLI
// itself on TypeScript 7.
//
// The plugins also expose a ReadonlyVisitor, which walks the AST without
// transforming it and serialises what it finds into a metadata.ts that the
// application loads at runtime. NestJS documents that path for its own SWC
// builder, because SWC cannot run TypeScript transformers either. nestgo takes
// the same route.
//
// A ReadonlyVisitor is handed a live ts.Program and returns ts.Node values, so
// it cannot be driven from Go. Generation therefore runs in a short-lived Node
// sidecar, and its output is compiled as an ordinary source file.
package plugins

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed generate-metadata.js
var sidecarScript []byte

// Options configures metadata generation.
type Options struct {
	// Cwd is the project root.
	Cwd string

	// TsConfigPath is the tsconfig the visitors build their program from.
	TsConfigPath string

	// OutputDir is where metadata.ts is written. This is the source root, not
	// the build output: the generated file is an input to compilation.
	OutputDir string

	// Filename overrides the default metadata.ts.
	Filename string

	// Plugins are the entries from nest-cli.json compilerOptions.plugins.
	Plugins []Plugin
}

// Plugin is one configured CLI plugin.
type Plugin struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options,omitempty"`
}

type sidecarResult struct {
	Written string `json:"written"`
	Error   string `json:"error"`
}

// Generate writes the plugin metadata file and returns its path. It returns an
// empty path when no plugins are configured.
func Generate(ctx context.Context, opts Options) (string, error) {
	if len(opts.Plugins) == 0 {
		return "", nil
	}

	node, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("NestJS CLI plugins need Node.js to generate metadata, but node is not on PATH")
	}

	script, cleanup, err := writeSidecar()
	if err != nil {
		return "", err
	}
	defer cleanup()

	config, err := json.Marshal(map[string]any{
		"cwd":          opts.Cwd,
		"tsconfigPath": opts.TsConfigPath,
		"outputDir":    opts.OutputDir,
		"filename":     opts.Filename,
		"plugins":      opts.Plugins,
	})
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, node, script, string(config))
	cmd.Dir = opts.Cwd
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, runErr := cmd.Output()

	var res sidecarResult
	if len(out) > 0 {
		_ = json.Unmarshal(out, &res)
	}
	if res.Error != "" {
		return "", fmt.Errorf("generating plugin metadata:\n%s", res.Error)
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return "", fmt.Errorf("generating plugin metadata: %s", detail)
	}

	return res.Written, nil
}

// writeSidecar materialises the embedded script so Node can run it.
func writeSidecar() (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "nestgo-plugins-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	path = filepath.Join(dir, "generate-metadata.js")
	if err := os.WriteFile(path, sidecarScript, 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
