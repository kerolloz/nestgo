module github.com/kerolloz/nestgo

go 1.26.2

replace (
	github.com/kerolloz/ttsgo => ../ttsgo
	github.com/microsoft/typescript-go/shim/ast => ../ttsgo/shim/ast
	github.com/microsoft/typescript-go/shim/bundled => ../ttsgo/shim/bundled
	github.com/microsoft/typescript-go/shim/compiler => ../ttsgo/shim/compiler
	github.com/microsoft/typescript-go/shim/core => ../ttsgo/shim/core
	github.com/microsoft/typescript-go/shim/tsoptions => ../ttsgo/shim/tsoptions
	github.com/microsoft/typescript-go/shim/vfs => ../ttsgo/shim/vfs
)

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/fsnotify/fsnotify v1.9.0
	github.com/kerolloz/ttsgo v0.0.0
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/microsoft/typescript-go v0.0.0-20260708042240-2bd066d87f5b // indirect
	github.com/microsoft/typescript-go/shim/ast v0.0.0 // indirect
	github.com/microsoft/typescript-go/shim/bundled v0.0.0 // indirect
	github.com/microsoft/typescript-go/shim/compiler v0.0.0 // indirect
	github.com/microsoft/typescript-go/shim/core v0.0.0 // indirect
	github.com/microsoft/typescript-go/shim/tsoptions v0.0.0 // indirect
	github.com/microsoft/typescript-go/shim/vfs v0.0.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)
