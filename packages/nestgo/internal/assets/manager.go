package assets

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"github.com/kerolloz/nestgo/internal/logger"
)

// Asset is a single asset entry.
type Asset struct {
	Glob        string
	Exclude     string
	OutDir      string
	WatchAssets bool
}

// Manager handles asset copying and optional watching.
type Manager struct {
	assets     []Asset
	sourceRoot string
	outDir     string
	cwd        string
	watcher    *fsnotify.Watcher
}

// New creates a new Manager.
func New(cwd, sourceRoot, outDir string, assets []Asset) (*Manager, error) {
	absRoot := absPath(cwd, sourceRoot)
	absOut := absPath(cwd, outDir)
	return &Manager{
		assets:     assets,
		sourceRoot: absRoot,
		outDir:     absOut,
		cwd:        cwd,
	}, nil
}

// Copy performs a one-shot copy of all assets.
func (m *Manager) Copy() error {
	for _, a := range m.assets {
		if err := m.copyAsset(a); err != nil {
			return err
		}
	}
	return nil
}

// Watch starts watching asset globs.
func (m *Manager) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	m.watcher = w

	if err := m.addRecursive(m.sourceRoot); err != nil {
		return err
	}

	go m.loop(ctx)
	return nil
}

func (m *Manager) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.watcher.Close()
			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}

			// Watch new directories
			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					if err := m.addRecursive(event.Name); err != nil {
						logger.Warn("Failed to add recursive watcher for %q: %v", event.Name, err)
					}
				}
			}

			for _, a := range m.assets {
				if m.matchesAsset(a, event.Name) {
					outDir := m.outDir
					if a.OutDir != "" {
						outDir = absPath(m.cwd, a.OutDir)
					}

					if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
						if err := removeAsset(event.Name, m.sourceRoot, outDir); err != nil {
							logger.Warn("Failed to remove asset %q: %v", event.Name, err)
						}
					} else {
						if err := copyFile(event.Name, m.sourceRoot, outDir); err != nil {
							logger.Warn("Failed to copy asset %q: %v", event.Name, err)
						}
					}
				}
			}
		case _, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (m *Manager) Close() {
	if m.watcher != nil {
		_ = m.watcher.Close()
	}
}

func (m *Manager) copyAsset(a Asset) error {
	outDir := m.outDir
	if a.OutDir != "" {
		outDir = absPath(m.cwd, a.OutDir)
	}

	absGlob := filepath.Join(m.sourceRoot, a.Glob)
	info, statErr := os.Stat(absGlob)
	if statErr == nil && info.IsDir() {
		return m.copyDir(absGlob, a.Exclude, outDir)
	}

	files, err := m.expandGlob(absGlob, a.Exclude)
	if err != nil {
		return fmt.Errorf("asset glob error for %q: %w", a.Glob, err)
	}

	for _, f := range files {
		if err := copyFile(f, m.sourceRoot, outDir); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) copyDir(dir, exclude, outDir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logger.Warn("WalkDir error at %q: %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if exclude != "" {
			exGlob := filepath.Join(m.sourceRoot, exclude)
			if excluded, _ := doublestar.PathMatch(filepath.ToSlash(exGlob), filepath.ToSlash(path)); excluded {
				return nil
			}
		}
		return copyFile(path, m.sourceRoot, outDir)
	})
}

func (m *Manager) expandGlob(pattern, exclude string) ([]string, error) {
	isWildcard := strings.HasSuffix(pattern, "*")
	matches, err := doublestar.FilepathGlob(pattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, err
	}

	var files []string
	for _, match := range matches {
		absMatch := match
		if exclude != "" {
			exGlob := filepath.Join(m.sourceRoot, exclude)
			if excluded, _ := doublestar.PathMatch(filepath.ToSlash(exGlob), filepath.ToSlash(absMatch)); excluded {
				continue
			}
		}

		info, err := os.Stat(absMatch)
		if err != nil {
			continue
		}

		if info.IsDir() && !isWildcard {
			err := filepath.WalkDir(absMatch, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				files = append(files, path)
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else if !info.IsDir() {
			files = append(files, absMatch)
		}
	}
	return files, nil
}

func (m *Manager) matchesAsset(a Asset, eventPath string) bool {
	absGlob := filepath.Join(m.sourceRoot, a.Glob)
	info, err := os.Stat(absGlob)
	if err == nil && info.IsDir() {
		if strings.HasPrefix(eventPath, absGlob+string(filepath.Separator)) || eventPath == absGlob {
			return !m.isExcluded(a, eventPath)
		}
		return false
	}
	matched, _ := doublestar.PathMatch(filepath.ToSlash(absGlob), filepath.ToSlash(eventPath))
	if matched {
		return !m.isExcluded(a, eventPath)
	}
	return false
}

func (m *Manager) isExcluded(a Asset, path string) bool {
	if a.Exclude == "" {
		return false
	}
	exGlob := filepath.Join(m.sourceRoot, a.Exclude)
	excluded, _ := doublestar.PathMatch(filepath.ToSlash(exGlob), filepath.ToSlash(path))
	return excluded
}

func copyFile(src, sourceRoot, outDir string) error {
	rel, err := filepath.Rel(sourceRoot, src)
	if err != nil {
		return err
	}
	dst := filepath.Join(outDir, rel)

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}

	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}

	return out.Close()
}

func removeAsset(src, sourceRoot, outDir string) error {
	rel, err := filepath.Rel(sourceRoot, src)
	if err != nil {
		return err
	}
	dst := filepath.Join(outDir, rel)
	return os.Remove(dst)
}

func (m *Manager) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			logger.Warn("WalkDir error at %q: %v", path, err)
			return nil
		}
		if d.IsDir() {
			if err := m.watcher.Add(path); err != nil {
				logger.Warn("Failed to watch %q: %v", path, err)
			}
		}
		return nil
	})
}

func absPath(cwd, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}
