// Package logger builds the server-side logrus loggers with consistent format and pluggable outputs.
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// New builds a configured logrus logger writing to the given output.
// level is a logrus level string ("trace", "debug", "info", "warn", "error"); empty defaults to "info".
// format is "text" (default) or "json".
// out may be nil; in that case os.Stdout is used.
func New(level, format string, out io.Writer) *logrus.Logger {
	l := logrus.New()
	if out == nil {
		out = os.Stdout
	}
	l.SetOutput(out)

	switch strings.ToLower(format) {
	case "json":
		l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339})
	default:
		l.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
			DisableQuote:    true,
		})
	}

	lv, err := logrus.ParseLevel(strings.TrimSpace(level))
	if err != nil || level == "" {
		lv = logrus.InfoLevel
	}
	l.SetLevel(lv)
	return l
}

// Open returns an io.Writer for a log destination string and a closer to call on shutdown.
//   - "" or "stdout"  -> os.Stdout
//   - "stderr"        -> os.Stderr
//   - any other value -> opened as a file (O_APPEND|O_CREATE|O_WRONLY, 0o644)
//
// The returned closer is always safe to call (no-op for stdout/stderr).
func Open(path string) (io.Writer, func() error, error) {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "", "stdout":
		return os.Stdout, func() error { return nil }, nil
	case "stderr":
		return os.Stderr, func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}
	return f, f.Close, nil
}

// Discard returns an io.Writer that drops all input (useful when silencing legacy loggers).
func Discard() io.Writer { return io.Discard }

// WithBaseFields installs a hook that injects the given fields on every log entry
// (without overwriting fields that the call site already set). Useful for tagging a
// dedicated logger with e.g. {"stream": "device"} so its output stays distinguishable
// even when multiple loggers share the same destination.
func WithBaseFields(l *logrus.Logger, base logrus.Fields) *logrus.Logger {
	l.AddHook(&baseFieldsHook{fields: base})
	return l
}

type baseFieldsHook struct{ fields logrus.Fields }

func (h *baseFieldsHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *baseFieldsHook) Fire(e *logrus.Entry) error {
	for k, v := range h.fields {
		if _, exists := e.Data[k]; !exists {
			e.Data[k] = v
		}
	}
	return nil
}

// Build is a small convenience that opens the output, builds a logger, and tags it
// with {"stream": stream}. The returned closer should be called on shutdown.
func Build(level, format, output, stream string) (*logrus.Logger, func() error, error) {
	out, closeFn, err := Open(output)
	if err != nil {
		return nil, nil, err
	}
	l := New(level, format, out)
	WithBaseFields(l, logrus.Fields{"stream": stream})
	return l, closeFn, nil
}
