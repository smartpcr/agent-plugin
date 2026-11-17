package cordis

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"
)

// LoggerServiceName is the service name the logger service is provided under.
const LoggerServiceName = "logger"

// LogLevel is the severity of a message, and the threshold of a logger. A logger
// writes a message when its level is at or below the threshold.
type LogLevel int

const (
	// LevelSilent, as a threshold, writes nothing.
	LevelSilent LogLevel = iota
	// LevelError marks failures that need attention.
	LevelError
	// LevelWarn marks unexpected conditions that do not stop the operation.
	LevelWarn
	// LevelInfo marks normal operational messages; the default threshold.
	LevelInfo
	// LevelDebug marks detailed diagnostics.
	LevelDebug
)

func (l LogLevel) String() string {
	switch l {
	case LevelSilent:
		return "Silent"
	case LevelError:
		return "Error"
	case LevelWarn:
		return "Warn"
	case LevelInfo:
		return "Info"
	case LevelDebug:
		return "Debug"
	default:
		return fmt.Sprintf("LogLevel(%d)", int(l))
	}
}

// LoggerConfig is the intercept config of the logger service:
// ctx.Intercept(cordis.LoggerServiceName, cordis.LoggerConfig{Level: ...}) changes
// the threshold of every logger below ctx.
type LoggerConfig struct {
	Level LogLevel
}

// LogMessage is one exported log line.
type LogMessage struct {
	Time  time.Time
	Name  string
	Level LogLevel
	Text  string
	Err   error
}

// Exporter receives log messages. Export must not block for long, and must not panic;
// a panic is swallowed.
type Exporter interface {
	Export(message LogMessage)
}

// ExporterFunc adapts a function to Exporter.
type ExporterFunc func(message LogMessage)

// Export calls f.
func (f ExporterFunc) Export(message LogMessage) { f(message) }

// LoggerService holds the exporters and default threshold of a tree. It is provided
// as the "logger" service.
type LoggerService struct {
	mu        sync.Mutex
	level     LogLevel
	exporters []*exporterEntry
}

type exporterEntry struct{ exporter Exporter }

func newLoggerService() *LoggerService {
	return &LoggerService{
		level:     LevelInfo,
		exporters: []*exporterEntry{{exporter: NewConsoleExporter(os.Stdout, os.Stderr)}},
	}
}

// Level returns the default threshold.
func (s *LoggerService) Level() LogLevel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.level
}

// SetLevel changes the default threshold.
func (s *LoggerService) SetLevel(level LogLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.level = level
}

// AddExporter adds an exporter and returns a function that removes it.
func (s *LoggerService) AddExporter(exporter Exporter) (remove func()) {
	entry := &exporterEntry{exporter: exporter}
	s.mu.Lock()
	s.exporters = append(s.exporters, entry)
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if i := slices.Index(s.exporters, entry); i >= 0 {
			s.exporters = slices.Delete(s.exporters, i, i+1)
		}
	}
}

// ClearExporters removes every exporter, including the default console exporter.
func (s *LoggerService) ClearExporters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exporters = nil
}

// For returns a logger with the given name whose threshold follows ctx's intercepts.
func (s *LoggerService) For(ctx *Context, name string) *Logger {
	return &Logger{service: s, ctx: ctx, name: name}
}

func (s *LoggerService) export(message LogMessage) {
	s.mu.Lock()
	entries := slices.Clone(s.exporters)
	s.mu.Unlock()
	for _, entry := range entries {
		func() {
			defer func() { _ = recover() }() // an exporter must never break the caller
			entry.exporter.Export(message)
		}()
	}
}

// Logger writes messages under one name.
type Logger struct {
	service *LoggerService
	ctx     *Context
	name    string
}

// Name returns the logger's name.
func (l *Logger) Name() string { return l.name }

// Error logs err. An error that unwraps to several errors (an AggregateError, or one
// made by errors.Join) is flattened: each member is logged on its own. One with no
// members is logged as itself.
func (l *Logger) Error(err error) {
	if err == nil {
		return
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		if members := multi.Unwrap(); len(members) > 0 {
			for _, member := range members {
				l.Error(member)
			}
			return
		}
	}
	l.write(LevelError, err.Error(), err)
}

// Errorf logs a formatted error message.
func (l *Logger) Errorf(format string, args ...any) {
	l.write(LevelError, fmt.Sprintf(format, args...), nil)
}

// Warnf logs a formatted warning.
func (l *Logger) Warnf(format string, args ...any) {
	l.write(LevelWarn, fmt.Sprintf(format, args...), nil)
}

// Infof logs a formatted informational message.
func (l *Logger) Infof(format string, args ...any) {
	l.write(LevelInfo, fmt.Sprintf(format, args...), nil)
}

// Debugf logs a formatted diagnostic message.
func (l *Logger) Debugf(format string, args ...any) {
	l.write(LevelDebug, fmt.Sprintf(format, args...), nil)
}

func (l *Logger) threshold() LogLevel {
	if config, ok := GetIntercept[LoggerConfig](l.ctx, LoggerServiceName); ok {
		return config.Level
	}
	if config, ok := GetIntercept[*LoggerConfig](l.ctx, LoggerServiceName); ok && config != nil {
		return config.Level
	}
	return l.service.Level()
}

func (l *Logger) write(level LogLevel, text string, err error) {
	if level > l.threshold() {
		return
	}
	l.service.export(LogMessage{Time: time.Now(), Name: l.name, Level: level, Text: text, Err: err})
}

// ------------------------------------------------------------------ exporters

// ConsoleExporter writes one line per message: errors and warnings to errOut, the
// rest to out.
type ConsoleExporter struct {
	mu  sync.Mutex
	out io.Writer
	err io.Writer
}

// NewConsoleExporter returns a console exporter writing to out and errOut.
func NewConsoleExporter(out, errOut io.Writer) *ConsoleExporter {
	return &ConsoleExporter{out: out, err: errOut}
}

// Export writes the message.
func (e *ConsoleExporter) Export(message LogMessage) {
	writer := e.out
	if message.Level <= LevelWarn {
		writer = e.err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintf(writer, "%s [%c] %s %s\n", message.Time.Format("15:04:05"), message.Level.String()[0], message.Name, message.Text)
	if panicError, ok := message.Err.(*PanicError); ok {
		fmt.Fprintf(writer, "%s\n", panicError.Stack)
	}
}

// MemoryExporter keeps every message, for tests and diagnostics.
type MemoryExporter struct {
	mu       sync.Mutex
	messages []LogMessage
}

// Export records the message.
func (e *MemoryExporter) Export(message LogMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, message)
}

// Messages returns a snapshot of the recorded messages.
func (e *MemoryExporter) Messages() []LogMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.messages)
}

// SlogExporter forwards messages to a log/slog handler.
type SlogExporter struct {
	Handler slog.Handler
}

// Export forwards the message as a slog record with a "logger" attribute.
func (e SlogExporter) Export(message LogMessage) {
	level := slog.LevelInfo
	switch message.Level {
	case LevelError:
		level = slog.LevelError
	case LevelWarn:
		level = slog.LevelWarn
	case LevelDebug:
		level = slog.LevelDebug
	}
	if !e.Handler.Enabled(context.Background(), level) {
		return
	}
	record := slog.NewRecord(message.Time, level, message.Text, 0)
	record.AddAttrs(slog.String("logger", message.Name))
	_ = e.Handler.Handle(context.Background(), record)
}
