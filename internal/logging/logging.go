package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger provides structured logging to a file
type Logger struct {
	file          *os.File
	mu            sync.Mutex
	prefix        string
	consoleOutput bool // Enable output to console (stderr/stdout)
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// InitLogger initializes the default logger
// If pluginRoot is empty, uses current directory
func InitLogger(pluginRoot string) (*Logger, error) {
	var err error
	once.Do(func() {
		if pluginRoot == "" {
			pluginRoot = "."
		}
		logPath := filepath.Join(pluginRoot, "notification-debug.log")
		defaultLogger, err = NewLogger(logPath)
	})
	return defaultLogger, err
}

// NewLogger creates a new logger that writes to the specified file
func NewLogger(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &Logger{
		file: f,
	}, nil
}

// NewLoggerFromFile creates a logger that writes to an already open file,
// such as an inherited stderr that the parent wired into its debug log.
func NewLoggerFromFile(f *os.File) *Logger {
	return &Logger{
		file: f,
	}
}

// SetPrefix sets a prefix for all log messages
func (l *Logger) SetPrefix(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prefix = prefix
}

// EnableConsoleOutput enables logging to console (stderr for errors/warnings, stdout for info/debug)
func (l *Logger) EnableConsoleOutput() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleOutput = true
}

// DisableConsoleOutput disables logging to console
func (l *Logger) DisableConsoleOutput() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleOutput = false
}

// log writes a formatted log message with timestamp
func (l *Logger) log(level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, args...)

	var logLine string
	if l.prefix != "" {
		logLine = fmt.Sprintf("[%s] [%s] %s: %s\n", timestamp, level, l.prefix, message)
	} else {
		logLine = fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, message)
	}

	// Write to file
	_, _ = l.file.WriteString(logLine)

	// Write to console if enabled
	if l.consoleOutput {
		// Use stderr for errors and warnings, stdout for info and debug
		var consoleOutput io.Writer
		if level == "ERROR" || level == "WARN" {
			consoleOutput = os.Stderr
		} else {
			consoleOutput = os.Stdout
		}

		// Add plugin prefix to console output for clarity
		var consoleLine string
		if l.prefix != "" {
			consoleLine = fmt.Sprintf("[claude-notifications] [%s] [%s] %s: %s\n", timestamp, level, l.prefix, message)
		} else {
			consoleLine = fmt.Sprintf("[claude-notifications] [%s] [%s] %s\n", timestamp, level, message)
		}
		_, _ = fmt.Fprint(consoleOutput, consoleLine)
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log("DEBUG", format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log("INFO", format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log("WARN", format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log("ERROR", format, args...)
}

// Close closes the log file
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// GetWriter returns the underlying writer for the logger
func (l *Logger) GetWriter() io.Writer {
	return l.file
}

// Global logger functions (use default logger)

// Debug logs a debug message using the default logger
func Debug(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Debug(format, args...)
	}
}

// Info logs an info message using the default logger
func Info(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Info(format, args...)
	}
}

// Warn logs a warning message using the default logger
func Warn(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Warn(format, args...)
	}
}

// Error logs an error message using the default logger
func Error(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Error(format, args...)
	}
}

// File returns the open file of the default logger, or nil if the logger
// has not been initialized. Child processes can inherit it as stderr.
func File() *os.File {
	if defaultLogger != nil {
		return defaultLogger.file
	}
	return nil
}

// SetPrefix sets a prefix for all log messages using the default logger
func SetPrefix(prefix string) {
	if defaultLogger != nil {
		defaultLogger.SetPrefix(prefix)
	}
}

// EnableConsoleOutput enables console output for the default logger
func EnableConsoleOutput() {
	if defaultLogger != nil {
		defaultLogger.EnableConsoleOutput()
	}
}

// DisableConsoleOutput disables console output for the default logger
func DisableConsoleOutput() {
	if defaultLogger != nil {
		defaultLogger.DisableConsoleOutput()
	}
}

// Close closes the default logger
func Close() error {
	if defaultLogger != nil {
		return defaultLogger.Close()
	}
	return nil
}
