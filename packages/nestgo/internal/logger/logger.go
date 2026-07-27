package logger

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Cyan   = "\033[36m"
	Gray   = "\033[90m"
)

// formatMessage creates a consistent, structured log line.
func formatMessage(level, color, msg string) string {
	timestamp := time.Now().Format("15:04:05")
	// Example: [14:30:22] [Nestgo] INFO  Compiling with tsgo...
	return fmt.Sprintf("%s[%s]%s %s[Nestgo]%s %s%-5s%s %s\n", Gray, timestamp, Reset, Cyan, Reset, color, level, Reset, msg)
}

// Info logs standard informational messages.
func Info(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprint(Stdout, formatMessage("INFO", Blue, msg))
}

// Warn logs warning messages.
func Warn(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprint(Stdout, formatMessage("WARN", Yellow, msg))
}

// Error logs error messages to stderr.
func Error(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprint(Stderr, formatMessage("ERROR", Red, msg))
}

// Success logs success/ready messages.
func Success(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprint(Stdout, formatMessage("READY", Green, msg))
}

// Step logs process steps or state changes (e.g., stopping process, watching).
func Step(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprint(Stdout, formatMessage("STEP", Cyan, msg))
}
