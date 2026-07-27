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
package cachedvfs

import (
	"github.com/microsoft/typescript-go/internal/vfs"
	inner "github.com/microsoft/typescript-go/internal/vfs/cachedvfs"
	_ "unsafe"
)

type FS = inner.FS

//go:linkname From github.com/microsoft/typescript-go/internal/vfs/cachedvfs.From
func From(fs vfs.FS) *FS
