package tsc

import (
	"strings"
	"testing"
)

func configWith(options map[string]any) *Config {
	cfg := &Config{Options: options}
	cfg.Paths = stringSliceMap(options["paths"])
	cfg.EmitDecoratorMetadata = boolOption(options, "emitDecoratorMetadata")
	cfg.ExperimentalDecorators = boolOption(options, "experimentalDecorators")
	return cfg
}

func problemFor(t *testing.T, problems []Problem, option string) Problem {
	t.Helper()
	for _, p := range problems {
		if p.Option == option {
			return p
		}
	}
	t.Fatalf("no problem reported for %q; got %+v", option, problems)
	return Problem{}
}

// Verbatim from @nestjs/schematics@11.1.0
// (dist/lib/application/files/ts/tsconfig.json), with the <%= strict %>
// placeholders resolved. This is the config a freshly generated NestJS app
// ships with, so it is what most users will arrive holding.
//
// It is already closer to TypeScript 7 than the older templates — module and
// moduleResolution are both "nodenext" — and baseUrl is the one setting that
// stops it compiling. Reporting anything else here would be a false alarm on
// a brand new project.
func TestPreflightOnStockNestTemplate(t *testing.T) {
	cfg := configWith(map[string]any{
		"module":                           "nodenext",
		"moduleResolution":                 "nodenext",
		"resolvePackageJsonExports":        true,
		"esModuleInterop":                  true,
		"isolatedModules":                  true,
		"declaration":                      true,
		"removeComments":                   true,
		"emitDecoratorMetadata":            true,
		"experimentalDecorators":           true,
		"allowSyntheticDefaultImports":     true,
		"target":                           "ES2023",
		"sourceMap":                        true,
		"outDir":                           "./dist",
		"baseUrl":                          "./",
		"incremental":                      true,
		"skipLibCheck":                     true,
		"strictNullChecks":                 true,
		"forceConsistentCasingInFileNames": true,
		"noImplicitAny":                    true,
		"strictBindCallApply":              true,
		"noFallthroughCasesInSwitch":       true,
	})

	problems := Preflight(cfg)
	if len(problems) != 1 {
		t.Fatalf("expected baseUrl to be the only problem, got %+v", problems)
	}
	if p := problemFor(t, problems, "baseUrl"); p.Fix == "" {
		t.Error("the baseUrl problem must say how to fix it — this is the first thing a new user hits")
	}

	if warnings := CheckNestJS(cfg); len(warnings) != 0 {
		t.Errorf("the template sets both decorator options; expected no warnings, got %+v", warnings)
	}
}

// Older NestJS projects — anything generated before the nodenext switch — do
// carry the removed resolution mode.
func TestPreflightOnLegacyNestConfig(t *testing.T) {
	cfg := configWith(map[string]any{
		"module":                 "commonjs",
		"moduleResolution":       "node",
		"target":                 "ES2017",
		"baseUrl":                "./",
		"emitDecoratorMetadata":  true,
		"experimentalDecorators": true,
	})

	problems := Preflight(cfg)
	for _, option := range []string{"baseUrl", "moduleResolution"} {
		if p := problemFor(t, problems, option); p.Fix == "" {
			t.Errorf("%s problem has no fix", option)
		}
	}
}

func TestPreflightNonRelativePaths(t *testing.T) {
	cfg := configWith(map[string]any{
		"paths": map[string]any{
			"@app/*":    []any{"src/app/*"},  // needs ./
			"@common/*": []any{"./common/*"}, // fine
		},
	})

	p := problemFor(t, Preflight(cfg), "paths")
	if !strings.Contains(p.Detail, "@app/*") {
		t.Errorf("detail should name the offending pattern: %s", p.Detail)
	}
	if strings.Contains(p.Detail, "@common/*") {
		t.Errorf("detail should not flag an already-relative target: %s", p.Detail)
	}
}

func TestPreflightAcceptsAValidConfig(t *testing.T) {
	cfg := configWith(map[string]any{
		"module":                 "commonjs",
		"target":                 "ES2022",
		"moduleResolution":       "bundler",
		"emitDecoratorMetadata":  true,
		"experimentalDecorators": true,
		"paths":                  map[string]any{"@app/*": []any{"./src/app/*"}},
	})

	if problems := Preflight(cfg); len(problems) != 0 {
		t.Errorf("expected no problems, got %+v", problems)
	}
}

func TestPreflightRemovedOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options map[string]any
		option  string
	}{
		{"es5 target", map[string]any{"target": "ES5"}, "target"},
		{"amd module", map[string]any{"module": "AMD"}, "module"},
		{"outFile", map[string]any{"outFile": "./bundle.js"}, "outFile"},
		{"classic resolution", map[string]any{"moduleResolution": "classic"}, "moduleResolution"},
		{"node10 resolution", map[string]any{"moduleResolution": "node10"}, "moduleResolution"},
		{"downlevelIteration", map[string]any{"downlevelIteration": true}, "downlevelIteration"},
		{"esModuleInterop off", map[string]any{"esModuleInterop": false}, "esModuleInterop"},
		{"alwaysStrict off", map[string]any{"alwaysStrict": false}, "alwaysStrict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := problemFor(t, Preflight(configWith(tc.options)), tc.option)
			if p.Detail == "" || p.Fix == "" {
				t.Errorf("problem should explain and advise: %+v", p)
			}
		})
	}
}

// Leaving these unset is fine; only an explicit false is an error.
func TestPreflightIgnoresUnsetInteropOptions(t *testing.T) {
	if problems := Preflight(configWith(map[string]any{"esModuleInterop": true})); len(problems) != 0 {
		t.Errorf("esModuleInterop: true is valid, got %+v", problems)
	}
	if problems := Preflight(configWith(map[string]any{})); len(problems) != 0 {
		t.Errorf("an empty config has nothing to migrate, got %+v", problems)
	}
}

// Missing decorator options compile cleanly and then break dependency
// injection at runtime, so they have to be surfaced separately.
func TestCheckNestJSMissingDecoratorOptions(t *testing.T) {
	warnings := CheckNestJS(configWith(map[string]any{"target": "ES2022"}))
	if len(warnings) != 2 {
		t.Fatalf("expected both decorator options flagged, got %+v", warnings)
	}

	joined := warnings[0].String() + "\n" + warnings[1].String()
	for _, want := range []string{"experimentalDecorators", "emitDecoratorMetadata"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should mention %s: %s", want, joined)
		}
	}
}

func TestProblemString(t *testing.T) {
	p := Problem{Option: "baseUrl", Detail: "removed", Fix: "drop it"}
	got := p.String()
	for _, want := range []string{"compilerOptions.baseUrl", "removed", "fix: drop it"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() missing %q: %s", want, got)
		}
	}
}
