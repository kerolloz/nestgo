package tsc

import (
	"fmt"
	"slices"
	"strings"
)

// Problem is a tsconfig setting TypeScript 7 will reject, together with what to
// do about it.
type Problem struct {
	// Option is the offending compilerOptions key.
	Option string

	// Detail says what is wrong.
	Detail string

	// Fix says how to resolve it.
	Fix string
}

func (p Problem) String() string {
	return fmt.Sprintf("compilerOptions.%s: %s\n  fix: %s", p.Option, p.Detail, p.Fix)
}

// Preflight reports tsconfig settings that TypeScript 7 removed.
//
// The compiler rejects these on its own, but its message names the option and
// stops. Most NestJS projects predate TypeScript 7 and hit several at once —
// the stock @nestjs/schematics template does not compile untouched — so the
// migration is the first thing a new user meets. Explaining it up front is the
// difference between a five-minute fix and a bug report.
//
// Preflight only reads the resolved config, so it works on projects the
// compiler would refuse to build.
func Preflight(cfg *Config) []Problem {
	var problems []Problem

	// baseUrl is gone; paths carry the resolution on their own now.
	if base := stringOption(cfg.Options, "baseUrl"); base != "" {
		problems = append(problems, Problem{
			Option: "baseUrl",
			Detail: "removed in TypeScript 7 (TS5102)",
			Fix:    `drop it and make every "paths" target relative, e.g. "paths": {"*": ["./*"]}`,
		})
	}

	// paths targets must be relative now that there is no baseUrl to anchor them.
	var nonRelative []string
	for pattern, targets := range cfg.Paths {
		for _, target := range targets {
			if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") {
				nonRelative = append(nonRelative, fmt.Sprintf("%q: %q", pattern, target))
			}
		}
	}
	if len(nonRelative) > 0 {
		slices.Sort(nonRelative) // map iteration order is random; keep output stable
		problems = append(problems, Problem{
			Option: "paths",
			Detail: "non-relative targets are not allowed (TS5090): " + strings.Join(nonRelative, ", "),
			Fix:    `prefix each target with "./"`,
		})
	}

	if resolution := strings.ToLower(stringOption(cfg.Options, "moduleResolution")); resolution != "" {
		if slices.Contains([]string{"node", "node10", "classic"}, resolution) {
			problems = append(problems, Problem{
				Option: "moduleResolution",
				Detail: fmt.Sprintf("%q was removed in TypeScript 7 (TS5108)", resolution),
				Fix:    `use "bundler" or "nodenext"`,
			})
		}
	}

	if target := strings.ToLower(stringOption(cfg.Options, "target")); target == "es5" || target == "es3" {
		problems = append(problems, Problem{
			Option: "target",
			Detail: fmt.Sprintf("%q is no longer supported; TypeScript 7 has no ES5 downlevel emit", target),
			Fix:    `raise it to "ES2015" or later — "ES2022" suits current Node`,
		})
	}

	if module := strings.ToLower(stringOption(cfg.Options, "module")); module != "" {
		if slices.Contains([]string{"amd", "system", "umd"}, module) {
			problems = append(problems, Problem{
				Option: "module",
				Detail: fmt.Sprintf("%q was removed in TypeScript 7", module),
				Fix:    `use "commonjs" or "nodenext"`,
			})
		}
	}

	if _, ok := cfg.Options["outFile"]; ok {
		problems = append(problems, Problem{
			Option: "outFile",
			Detail: "removed in TypeScript 7",
			Fix:    "emit separate files with outDir, and bundle afterwards if you need one file",
		})
	}

	// These three are errors only when explicitly disabled; leaving them unset
	// is fine, which is why the check is for a literal false.
	for _, option := range []string{"esModuleInterop", "allowSyntheticDefaultImports", "alwaysStrict"} {
		if value, ok := cfg.Options[option].(bool); ok && !value {
			problems = append(problems, Problem{
				Option: option,
				Detail: "cannot be disabled in TypeScript 7",
				Fix:    "remove the override",
			})
		}
	}

	if _, ok := cfg.Options["downlevelIteration"]; ok {
		problems = append(problems, Problem{
			Option: "downlevelIteration",
			Detail: "removed in TypeScript 7; iteration is native at ES2015 and above",
			Fix:    "remove it",
		})
	}

	return problems
}

// NestJSWarning is advice about a setting NestJS needs that the compiler
// accepts without comment.
type NestJSWarning struct {
	Option string
	Detail string
}

func (w NestJSWarning) String() string {
	return fmt.Sprintf("compilerOptions.%s: %s", w.Option, w.Detail)
}

// CheckNestJS reports settings a NestJS application needs. These are not
// compiler errors — the build succeeds and dependency injection then fails at
// runtime, which is a far worse way to find out.
func CheckNestJS(cfg *Config) []NestJSWarning {
	var warnings []NestJSWarning

	if !cfg.ExperimentalDecorators {
		warnings = append(warnings, NestJSWarning{
			Option: "experimentalDecorators",
			Detail: "not enabled; NestJS decorators require it",
		})
	}
	if !cfg.EmitDecoratorMetadata {
		warnings = append(warnings, NestJSWarning{
			Option: "emitDecoratorMetadata",
			Detail: "not enabled; NestJS resolves constructor dependencies from this metadata",
		})
	}

	return warnings
}
