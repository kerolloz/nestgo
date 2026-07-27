package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kerolloz/nestgo/internal/logger"
)

// ProcessRunner defines the interface for managing the child process.
type ProcessRunner interface {
	Start() error
	Kill()
	Wait() int
}

// Options configures the Node.js child process.
type Options struct {
	EntryFile  string
	SourceRoot string
	RootDir    string
	OutDir     string
	Binary     string
	Debug      string
	EnvFiles   []string
	ExtraArgs  []string
	Shell      bool
	Cwd        string
}

// procState holds the lifecycle of a single child process invocation.
// waitOnce ensures cmd.Wait() is called exactly once regardless of
// whether Kill or Wait reaches it first.
type procState struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	waitCode int
	done     chan struct{}
}

func (s *procState) wait() {
	s.waitOnce.Do(func() {
		err := s.cmd.Wait()
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.waitCode = exitErr.ExitCode()
		}
		close(s.done)
	})
}

// Runner manages a single Node.js child process and supports hot-reload.
type Runner struct {
	opts  Options
	mu    sync.Mutex
	state *procState
}

// New creates a Runner with the given options.
func New(opts Options) ProcessRunner {
	if opts.Binary == "" {
		opts.Binary = "node"
	}
	if opts.Cwd == "" {
		opts.Cwd, _ = os.Getwd()
	}
	return &Runner{opts: opts}
}

// Start spawns the Node.js process.
func (r *Runner) Start() error {
	cmd, err := r.build()
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return err
	}
	r.state = &procState{cmd: cmd, done: make(chan struct{})}
	return nil
}

// Kill terminates the process tree and waits for it to fully exit.
func (r *Runner) Kill() {
	r.mu.Lock()
	state := r.state
	r.state = nil
	r.mu.Unlock()

	if state == nil || state.cmd.Process == nil {
		return
	}
	killTree(state.cmd.Process.Pid)
	state.wait()
}

// Wait blocks until the child process exits and returns its exit code.
func (r *Runner) Wait() int {
	r.mu.Lock()
	state := r.state
	r.mu.Unlock()

	if state == nil {
		return 0
	}
	state.wait()
	<-state.done
	return state.waitCode
}

func (r *Runner) build() (*exec.Cmd, error) {
	outputFile := r.resolveOutputFile()
	if outputFile == "" {
		return nil, fmt.Errorf(
			"could not find entry file. Looked for %s/%s/%s.js, %s/%s/%s.js, and %s/%s.js",
			r.opts.OutDir, r.opts.SourceRoot, r.opts.EntryFile,
			r.opts.OutDir, r.opts.RootDir, r.opts.EntryFile,
			r.opts.OutDir, r.opts.EntryFile,
		)
	}

	var nodeArgs []string
	if r.opts.Debug != "" {
		if r.opts.Debug == "true" {
			nodeArgs = append(nodeArgs, "--inspect")
		} else {
			nodeArgs = append(nodeArgs, "--inspect="+r.opts.Debug)
		}
	}
	for _, ef := range r.opts.EnvFiles {
		nodeArgs = append(nodeArgs, "--env-file="+ef)
	}
	nodeArgs = append(nodeArgs, "--enable-source-maps", "--experimental-specifier-resolution=node", outputFile)
	nodeArgs = append(nodeArgs, r.opts.ExtraArgs...)

	var cmd *exec.Cmd
	if r.opts.Shell {
		allParts := append([]string{r.opts.Binary}, nodeArgs...)
		quoted := make([]string, len(allParts))
		for i, p := range allParts {
			quoted[i] = quoteShellArg(p)
		}
		cmd = exec.Command("sh", "-c", strings.Join(quoted, " "))
	} else {
		cmd = exec.Command(r.opts.Binary, nodeArgs...)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = r.opts.Cwd
	setSysProcAttr(cmd)
	return cmd, nil
}

func (r *Runner) resolveOutputFile() string {
	// OutDir and RootDir may be absolute (the compiler reports them that way),
	// in which case joining them onto Cwd would produce a path that does not
	// exist.
	outDir := r.opts.OutDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(r.opts.Cwd, outDir)
	}
	rootDir := r.opts.RootDir
	if filepath.IsAbs(rootDir) {
		if rel, err := filepath.Rel(r.opts.Cwd, rootDir); err == nil {
			rootDir = rel
		}
	}

	seen := map[string]bool{}
	candidates := []string{
		filepath.Join(outDir, r.opts.SourceRoot, r.opts.EntryFile+".js"),
		filepath.Join(outDir, rootDir, r.opts.EntryFile+".js"),
		filepath.Join(outDir, r.opts.EntryFile+".js"),
	}

	var found []string
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		if _, err := os.Stat(c); err == nil {
			found = append(found, c)
		}
	}

	if len(found) > 1 {
		logger.Warn("Multiple entry file candidates found, using: %s", found[0])
		for _, f := range found[1:] {
			logger.Warn("  Also found: %s", f)
		}
	}

	if len(found) == 0 {
		return ""
	}
	return found[0]
}

// quoteShellArg always single-quotes: a blacklist of special characters is
// too easy to get wrong (backticks were once missed, enabling command
// substitution via sh -c).
func quoteShellArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
