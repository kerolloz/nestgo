// Shim into typescript-go's internal packages. See ARCHITECTURE.md §3.
//
// This file contains only type aliases, which the compiler does check. The
// linkname to ast.GetNodeAtPosition was removed as unused — it also carried an
// upstream hazard (Corsa reports node positions as UTF-8 offsets, not UTF-16).
package ast

import (
	inner "github.com/microsoft/typescript-go/internal/ast"
)

type Diagnostic = inner.Diagnostic
type SourceFile = inner.SourceFile
type Node = inner.Node

const (
	KindFunctionDeclaration = inner.KindFunctionDeclaration
)
