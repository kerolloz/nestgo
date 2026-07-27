package paths

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Rewriter resolves tsconfig path aliases in emitted JS source text.
type Rewriter struct {
	cwd        string
	absOutDir  string
	sourceBase string
	pathsBase  string // base dir that `paths` targets resolve against
	matchers   []pathMatcher
	statCache  sync.Map // caches os.Stat results: path -> bool (isDir)

	// packageCache caches node_modules lookups: package name -> installed.
	packageCache sync.Map
}

type pathMatcher struct {
	pattern     string
	prefix      string
	hasWildcard bool
	targets     []string
}

// requireRe matches require("...") and dynamic import("...").
var requireRe = regexp.MustCompile(`(require\(["']|import\s*\(\s*["'])([^"'\n]+)(["'])`)

// fromRe matches static import/export ... from "..." including multiline.
var fromRe = regexp.MustCompile(`(?s)((?:import|export)[^"']*?from\s+["'])([^"']+)(["'])`)

type match struct {
	start, end               int
	prefix, specifier, quote string
}

func collectMatches(re *regexp.Regexp, content string) []match {
	raw := re.FindAllStringSubmatchIndex(content, -1)
	if len(raw) == 0 {
		return nil
	}
	out := make([]match, 0, len(raw))
	for _, m := range raw {
		out = append(out, match{
			start:     m[0],
			end:       m[1],
			prefix:    content[m[2]:m[3]],
			specifier: content[m[4]:m[5]],
			quote:     content[m[6]:m[7]],
		})
	}
	return out
}

// Options configures a Rewriter. Every directory may be absolute or relative
// to Cwd.
type Options struct {
	Cwd     string
	Paths   map[string][]string // tsconfig compilerOptions.paths
	OutDir  string              // where emitted files land
	RootDir string              // tsconfig rootDir; the source tree that OutDir mirrors

	// PathsBase is the directory that Paths targets resolve against. Under
	// TypeScript this is baseUrl when set, otherwise the directory of the
	// tsconfig that declared the paths — which is not necessarily Cwd, e.g.
	// when compiling -p configs/tsconfig.json. Empty means Cwd.
	PathsBase string
}

// New creates a Rewriter.
func New(opts Options) *Rewriter {
	resolve := func(p, fallback string) string {
		if p == "" {
			return fallback
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(opts.Cwd, p)
	}

	r := &Rewriter{
		cwd:        opts.Cwd,
		absOutDir:  resolve(opts.OutDir, opts.Cwd),
		sourceBase: resolve(opts.RootDir, opts.Cwd),
		pathsBase:  resolve(opts.PathsBase, opts.Cwd),
	}
	r.buildMatchers(opts.Paths)
	return r
}

func (r *Rewriter) buildMatchers(paths map[string][]string) {
	for pattern, targets := range paths {
		m := pathMatcher{pattern: pattern, targets: targets}
		if strings.HasSuffix(pattern, "/*") {
			m.hasWildcard = true
			m.prefix = strings.TrimSuffix(pattern, "/*")
		} else if strings.HasSuffix(pattern, "*") {
			m.hasWildcard = true
			m.prefix = strings.TrimSuffix(pattern, "*")
		} else {
			m.prefix = pattern
		}
		r.matchers = append(r.matchers, m)
	}
}

// RewriteSource takes emitted JS text and rewrites path aliases to relative paths.
func (r *Rewriter) RewriteSource(fileName, text string) string {
	result, _ := r.Rewrite(fileName, text)
	return result
}

// Rewrite is RewriteSource plus the edits it made, which AdjustSourceMap needs
// to keep the companion .js.map pointing at the right columns.
func (r *Rewriter) Rewrite(fileName, text string) (string, []Edit) {
	all := collectMatches(requireRe, text)
	all = append(all, collectMatches(fromRe, text)...)
	if len(all) == 0 {
		return text, nil
	}

	regions := skipRegions(text)
	if len(regions) > 0 {
		filtered := all[:0]
		for _, m := range all {
			if !isInSkipRegion(m.start, regions) {
				filtered = append(filtered, m)
			}
		}
		all = filtered
		if len(all) == 0 {
			return text, nil
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].start < all[j].start })

	var sb strings.Builder
	sb.Grow(len(text))
	lastPos := 0
	modified := false
	var edits []Edit

	for _, m := range all {
		if m.start < lastPos {
			continue
		}
		sb.WriteString(text[lastPos:m.start])

		resolved, ok := r.resolveAlias(m.specifier, fileName)
		if !ok {
			// If not an alias, check if it's a relative import that needs a .js extension
			if isRelative(m.specifier) && !hasExtension(m.specifier) {
				resolved, ok = r.resolveFileOrDir(m.specifier, fileName)
			}
		}

		if ok {
			sb.WriteString(m.prefix)
			sb.WriteString(resolved)
			sb.WriteString(m.quote)
			modified = true

			if delta := len(resolved) - len(m.specifier); delta != 0 {
				line, col := positionOf(text, m.start+len(m.prefix))
				edits = append(edits, Edit{Line: line, Column: col, Delta: delta})
			}
		} else {
			sb.WriteString(text[m.start:m.end])
		}
		lastPos = m.end
	}
	sb.WriteString(text[lastPos:])

	if !modified {
		return text, nil
	}
	return sb.String(), edits
}

// positionOf converts a byte offset into 0-based line and column.
func positionOf(text string, offset int) (line, col int) {
	if offset > len(text) {
		offset = len(text)
	}
	lastNewline := strings.LastIndexByte(text[:offset], '\n')
	line = strings.Count(text[:offset], "\n")
	return line, offset - (lastNewline + 1)
}

func (r *Rewriter) resolveAlias(specifier, fromFile string) (string, bool) {
	if strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "/") {
		return "", false
	}

	// An installed package wins over an alias that happens to match its name.
	// A tsconfig pattern like "@app/*" also matches the real package
	// "@app/thing" once someone installs it, and rewriting that to a relative
	// path breaks the import (nest-cli#838).
	if r.isInstalledPackage(specifier) {
		return "", false
	}

	for _, m := range r.matchers {
		remainder, matched := matchPattern(m, specifier)
		if !matched {
			continue
		}
		for _, tpl := range m.targets {
			tplClean := filepath.FromSlash(tpl)
			var sourceTargetAbs string
			if m.hasWildcard {
				tplBase := strings.TrimSuffix(strings.TrimSuffix(tplClean, "/*"), "*")
				sourceTargetAbs = filepath.Join(r.pathsBase, tplBase, remainder)
			} else {
				sourceTargetAbs = filepath.Join(r.pathsBase, tplClean)
			}

			rel, err := filepath.Rel(r.sourceBase, sourceTargetAbs)
			if err != nil || strings.HasPrefix(rel, "..") {
				rel, err = filepath.Rel(r.cwd, sourceTargetAbs)
				if err != nil {
					continue
				}
			}
			outputTargetAbs := filepath.Join(r.absOutDir, rel)
			fromDir := filepath.Dir(fromFile)
			relPath, err := filepath.Rel(fromDir, outputTargetAbs)
			if err != nil {
				continue
			}
			relPath = filepath.ToSlash(relPath)
			if !strings.HasPrefix(relPath, ".") {
				relPath = "./" + relPath
			}

			// Auto-append .js extension or /index.js if missing
			if !hasExtension(relPath) {
				relPath, _ = r.resolveFileOrDir(relPath, fromFile)
			}

			return relPath, true
		}
	}
	return "", false
}

// isInstalledPackage reports whether specifier names a package present in
// node_modules, searching upward from the project so workspace and hoisted
// layouts resolve the same way Node would.
func (r *Rewriter) isInstalledPackage(specifier string) bool {
	pkg := packageNameOf(specifier)
	if pkg == "" {
		return false
	}

	if cached, ok := r.packageCache.Load(pkg); ok {
		return cached.(bool)
	}

	found := false
	for dir := r.cwd; ; {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", filepath.FromSlash(pkg))); err == nil {
			found = true
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	r.packageCache.Store(pkg, found)
	return found
}

// packageNameOf extracts the package portion of a bare specifier:
// "lodash/fp" -> "lodash", "@nestjs/common/decorators" -> "@nestjs/common".
func packageNameOf(specifier string) string {
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") {
		if len(parts) < 2 {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func matchPattern(m pathMatcher, specifier string) (string, bool) {
	if m.hasWildcard {
		if specifier == m.prefix {
			return "", true
		}
		if strings.HasPrefix(specifier, m.prefix+"/") {
			return strings.TrimPrefix(specifier, m.prefix+"/"), true
		}
	} else if specifier == m.pattern {
		return "", true
	}
	return "", false
}

func (r *Rewriter) resolveFileOrDir(specifier, fromFile string) (string, bool) {
	// Map fromFile (output) to source file to check directory existence
	relToOut, err := filepath.Rel(r.absOutDir, fromFile)
	var absSourcePath string
	if err == nil && !strings.HasPrefix(relToOut, "..") {
		absSourcePath = filepath.Join(r.sourceBase, filepath.Dir(relToOut), specifier)
	} else {
		// Fallback to output dir check (for assets/etc)
		absSourcePath = filepath.Join(filepath.Dir(fromFile), specifier)
	}

	// Check cached stat result
	if isDir, ok := r.statCache.Load(absSourcePath); ok {
		if isDir.(bool) {
			return strings.TrimSuffix(specifier, "/") + "/index.js", true
		}
		return specifier + ".js", true
	}

	// Check if it's a directory
	info, err := os.Stat(absSourcePath)
	if err == nil && info.IsDir() {
		r.statCache.Store(absSourcePath, true)
		return strings.TrimSuffix(specifier, "/") + "/index.js", true
	}

	r.statCache.Store(absSourcePath, false)
	return specifier + ".js", true
}

func isRelative(specifier string) bool {
	return strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../")
}

func hasExtension(path string) bool {
	ext := filepath.Ext(path)
	switch ext {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts", ".json":
		return true
	default:
		return false
	}
}
