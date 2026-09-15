package errorhandler

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"

	"github.com/777genius/agent-notifications/internal/logging"
)

// ErrorHandler provides global error handling and logging
type ErrorHandler struct {
	mu              sync.Mutex
	logToConsole    bool
	exitOnCritical  bool
	recoveryEnabled bool
}

var (
	defaultHandler *ErrorHandler
	handlerOnce    sync.Once
)

// Init initializes the global error handler with custom settings
// If handler is already initialized, returns the existing handler
func Init(logToConsole, exitOnCritical, recoveryEnabled bool) *ErrorHandler {
	// Use handlerOnce to ensure only one initialization
	handlerOnce.Do(func() {
		defaultHandler = &ErrorHandler{
			logToConsole:    logToConsole,
			exitOnCritical:  exitOnCritical,
			recoveryEnabled: recoveryEnabled,
		}

		// Enable console output in logging if requested
		if logToConsole {
			logging.EnableConsoleOutput()
		}
	})
	return defaultHandler
}

// GetHandler returns the default error handler (auto-initializes with defaults if needed)
func GetHandler() *ErrorHandler {
	// Use handlerOnce to ensure thread-safe initialization
	// This prevents data races when multiple goroutines call GetHandler concurrently
	handlerOnce.Do(func() {
		// Only init if not already done by explicit Init() call
		defaultHandler = &ErrorHandler{
			logToConsole:    true,
			exitOnCritical:  false,
			recoveryEnabled: true,
		}
		// Enable console output in logging
		logging.EnableConsoleOutput()
	})
	return defaultHandler
}

// Reset resets the error handler (for testing only)
// WARNING: This is not thread-safe and should only be called in tests
// when no other goroutines are using the error handler
func Reset() {
	defaultHandler = nil
	handlerOnce = sync.Once{}
}

// HandleError handles a general error
func (h *ErrorHandler) HandleError(err error, context string) {
	if err == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	message := fmt.Sprintf("%s: %v", context, err)

	// Log to file (and console if enabled via logging package)
	logging.Error("%s", message)
}

// HandleCriticalError handles a critical error that may require program termination
func (h *ErrorHandler) HandleCriticalError(err error, context string) {
	if err == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	message := fmt.Sprintf("CRITICAL ERROR - %s: %v", context, err)

	// Log to file (and console if enabled via logging package)
	logging.Error("%s", message)

	// Duplicate critical errors on stderr for interactive/Claude routes.
	// Observation routes (logToConsole=false, e.g. Codex hooks) must keep
	// process output empty, so the file log is the only sink there.
	if h.logToConsole {
		fmt.Fprintf(os.Stderr, "[claude-notifications] %s\n", message)
	}

	if h.exitOnCritical {
		os.Exit(1)
	}
}

// HandlePanic recovers from a panic and logs it
func (h *ErrorHandler) HandlePanic() {
	if !h.recoveryEnabled {
		return
	}

	if r := recover(); r != nil {
		h.mu.Lock()
		defer h.mu.Unlock()

		message := fmt.Sprintf("PANIC RECOVERED: %v\n%s", r, debug.Stack())

		// Log to file (and console if enabled via logging package)
		logging.Error("%s", message)

		// Panics reach stderr only on interactive/Claude routes; observation
		// routes (logToConsole=false) stay silent and rely on the file log.
		if h.logToConsole {
			fmt.Fprintf(os.Stderr, "[claude-notifications] PANIC: %v\n", r)
		}

		if h.exitOnCritical {
			os.Exit(1)
		}
	}
}

// Warn logs a warning message
func (h *ErrorHandler) Warn(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logging.Warn("%s", message)
}

// Info logs an informational message
func (h *ErrorHandler) Info(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logging.Info("%s", message)
}

// Debug logs a debug message
func (h *ErrorHandler) Debug(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	logging.Debug("%s", message)
}

// Global convenience functions

// HandleError handles a general error using the default handler
func HandleError(err error, context string) {
	GetHandler().HandleError(err, context)
}

// HandleCriticalError handles a critical error using the default handler
func HandleCriticalError(err error, context string) {
	GetHandler().HandleCriticalError(err, context)
}

// HandlePanic recovers from a panic using the default handler
func HandlePanic() {
	GetHandler().HandlePanic()
}

// Warn logs a warning using the default handler
func Warn(format string, args ...interface{}) {
	GetHandler().Warn(format, args...)
}

// Info logs an info message using the default handler
func Info(format string, args ...interface{}) {
	GetHandler().Info(format, args...)
}

// Debug logs a debug message using the default handler
func Debug(format string, args ...interface{}) {
	GetHandler().Debug(format, args...)
}

// WithRecovery wraps a function with panic recovery
func WithRecovery(fn func()) {
	defer HandlePanic()
	fn()
}

// WithRecoveryFunc wraps a function that returns an error with panic recovery
func WithRecoveryFunc(fn func() error) error {
	defer HandlePanic()
	return fn()
}

// SafeGo runs a goroutine with panic recovery
func SafeGo(fn func()) {
	go WithRecovery(fn)
}
