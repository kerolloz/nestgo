// Shim into typescript-go's internal packages. See ARCHITECTURE.md §3.
//
// UNCHECKED BOUNDARY: the go:linkname declarations below are NOT type-checked
// against upstream. If a signature changes upstream, this package still compiles
// cleanly, passes go vet, and then segfaults or silently returns garbage at
// runtime. That has already happened twice on version bumps. The only check that
// catches it is the end-to-end CI job that compiles a project and runs the output.
//
// Rules while this exists:
//   - pin github.com/microsoft/typescript-go to a typescript/vX.Y.Z release tag only
//   - re-verify every declaration below against the pinned source on any bump
//
// This tree is slated for deletion in Phase 2 (subprocess architecture).
package tsoptions

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/core"
	inner "github.com/microsoft/typescript-go/internal/tsoptions"
	_ "unsafe"
)

var _ = inner.GetParsedCommandLineOfConfigFile

type ParsedCommandLine = inner.ParsedCommandLine
type ParseConfigHost = inner.ParseConfigHost
type ExtendedConfigCache = inner.ExtendedConfigCache

//go:linkname GetParsedCommandLineOfConfigFile github.com/microsoft/typescript-go/internal/tsoptions.GetParsedCommandLineOfConfigFile
func GetParsedCommandLineOfConfigFile(configFileName string, options *core.CompilerOptions, optionsRaw *collections.OrderedMap[string, any], sys inner.ParseConfigHost, extendedConfigCache inner.ExtendedConfigCache) (*inner.ParsedCommandLine, []*ast.Diagnostic)
