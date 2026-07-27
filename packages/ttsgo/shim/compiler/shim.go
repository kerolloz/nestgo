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
package compiler

import (
	"context"
	"github.com/microsoft/typescript-go/internal/ast"
	inner "github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs"
	_ "unsafe"
)

var _ = inner.NewProgram

type CompilerHost = inner.CompilerHost
type EmitOptions = inner.EmitOptions
type EmitResult = inner.EmitResult
type Program = inner.Program
type ProgramOptions = inner.ProgramOptions
type WriteFile = inner.WriteFile
type WriteFileData = inner.WriteFileData

//go:linkname NewProgram github.com/microsoft/typescript-go/internal/compiler.NewProgram
func NewProgram(opts inner.ProgramOptions) *inner.Program

//go:linkname NewCompilerHost github.com/microsoft/typescript-go/internal/compiler.NewCompilerHost
func NewCompilerHost(currentDirectory string, fs vfs.FS, defaultLibraryPath string, extendedConfigCache tsoptions.ExtendedConfigCache, trace func(msg *diagnostics.Message, args ...any)) inner.CompilerHost

//go:linkname SortAndDeduplicateDiagnostics github.com/microsoft/typescript-go/internal/compiler.SortAndDeduplicateDiagnostics
func SortAndDeduplicateDiagnostics(diagnostics []*ast.Diagnostic) []*ast.Diagnostic

// NOTE: linkname bypasses type checking — this signature must exactly match
// the pinned typescript-go version (7.1.0-dev on main changes `file` to
// `files []*ast.SourceFile`; update this when bumping past v7.0.x).
//
//go:linkname GetDiagnosticsOfAnyProgram github.com/microsoft/typescript-go/internal/compiler.GetDiagnosticsOfAnyProgram
func GetDiagnosticsOfAnyProgram(ctx context.Context, program inner.ProgramLike, file *ast.SourceFile, skipNoEmitCheckForDtsDiagnostics bool, getBindDiagnostics func(context.Context, *ast.SourceFile) []*ast.Diagnostic, getSemanticDiagnostics func(context.Context, *ast.SourceFile) []*ast.Diagnostic) []*ast.Diagnostic
