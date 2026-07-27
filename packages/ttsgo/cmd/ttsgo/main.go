package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kerolloz/ttsgo/pkg/engine"
	"github.com/kerolloz/ttsgo/pkg/toolchain"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("ttsgo", flag.ExitOnError)
	project := fs.String("p", "tsconfig.json", "Path to tsconfig.json")
	outDir := fs.String("outDir", "", "Redirect output structure to the directory")
	noEmit := fs.Bool("noEmit", false, "Do not emit outputs")
	incremental := fs.Bool("incremental", true, "Reuse .tsbuildinfo to speed up rebuilds")
	showVersion := fs.Bool("version", false, "Print version and exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	compiler, err := toolchain.Locate(cwd)
	if err != nil {
		return err
	}

	ctx := context.Background()
	fmt.Printf("Compiling %s with TypeScript %s...\n", *project, compiler.Version)

	result, err := engine.CompileWithRewrite(ctx, engine.Options{
		Cwd:          cwd,
		TsConfigPath: *project,
		OutDir:       *outDir,
		Emit:         !*noEmit,
		Bin:          compiler.Path,
		Incremental:  *incremental,
	})
	if err != nil {
		return err
	}

	// Config problems are reported before diagnostics: they explain most of the
	// errors that follow, and the compiler's own message does not say how to
	// fix them.
	for _, p := range result.Problems {
		fmt.Fprintf(os.Stderr, "%s\n", p)
	}

	if len(result.Diagnostics) > 0 {
		for _, d := range result.Diagnostics {
			fmt.Fprintln(os.Stderr, d)
		}
		fmt.Fprintf(os.Stderr, "\nFound %d error(s).\n", len(result.Diagnostics))
		os.Exit(1)
	}

	if *noEmit {
		fmt.Println("Check complete, no errors.")
	} else {
		fmt.Printf("Emitted %d files.\n", len(result.EmittedFiles))
	}
	return nil
}
