package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kerolloz/ttsgo/pkg/engine"
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

	ctx := context.Background()
	opts := engine.Options{
		Cwd:          cwd,
		TsConfigPath: *project,
		OutDir:       *outDir,
		Emit:         !*noEmit,
	}

	fmt.Printf("Compiling %s...\n", *project)
	result, err := engine.CompileWithRewrite(ctx, opts)
	if err != nil {
		return err
	}

	if len(result.Diagnostics) > 0 {
		fmt.Println("Diagnostics found:")
		for _, d := range result.Diagnostics {
			fmt.Printf("  %s\n", d)
		}
		os.Exit(1)
	}

	if opts.Emit {
		fmt.Printf("Emitted %d files.\n", len(result.EmittedFiles))
	} else {
		fmt.Println("Check complete, no errors.")
	}

	return nil
}
