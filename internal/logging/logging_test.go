package logging

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewLogger(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	if logger == nil {
		t.Fatal("NewLogger() returned nil logger")
	}

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", logPath)
	}
}

func TestNewLogger_InvalidPath(t *testing.T) {
	// Try to create logger in non-existent directory
	_, err := NewLogger("/nonexistent/path/test.log")
	if err == nil {
		t.Error("NewLogger() should return error for invalid path")
	}
}

func TestLogger_Debug(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "debug.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.Debug("test debug message: %s", "value")

	// Read log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[DEBUG]") {
		t.Errorf("Log should contain [DEBUG] level, got: %s", logContent)
	}
	if !strings.Contains(logContent, "test debug message: value") {
		t.Errorf("Log should contain message, got: %s", logContent)
	}
}

func TestLogger_Info(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "info.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.Info("test info: %d", 42)

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[INFO]") {
		t.Errorf("Log should contain [INFO] level")
	}
	if !strings.Contains(logContent, "test info: 42") {
		t.Errorf("Log should contain message")
	}
}

func TestLogger_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "warn.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.Warn("warning message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[WARN]") {
		t.Errorf("Log should contain [WARN] level")
	}
	if !strings.Contains(logContent, "warning message") {
		t.Errorf("Log should contain message")
	}
}

func TestLogger_Error(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "error.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.Error("error occurred: %s", "test error")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[ERROR]") {
		t.Errorf("Log should contain [ERROR] level")
	}
	if !strings.Contains(logContent, "error occurred: test error") {
		t.Errorf("Log should contain message")
	}
}

func TestLogger_SetPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "prefix.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.SetPrefix("MODULE")
	logger.Info("message with prefix")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "MODULE:") {
		t.Errorf("Log should contain prefix 'MODULE:', got: %s", logContent)
	}
}

func TestLogger_EnableDisableConsoleOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "console.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	// Test enable
	logger.EnableConsoleOutput()
	if !logger.consoleOutput {
		t.Error("EnableConsoleOutput() should set consoleOutput to true")
	}

	// Test disable
	logger.DisableConsoleOutput()
	if logger.consoleOutput {
		t.Error("DisableConsoleOutput() should set consoleOutput to false")
	}
}

func TestLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "close.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	err = logger.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Close again - file is already closed, so err is expected
	// Just verify it doesn't panic
	_ = logger.Close()
}

func TestLogger_GetWriter(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "writer.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	writer := logger.GetWriter()
	if writer == nil {
		t.Error("GetWriter() should return non-nil writer")
	}
}

func TestInitLogger(t *testing.T) {
	tmpDir := t.TempDir()

	// Reset defaultLogger for this test
	defaultLogger = nil
	once = sync.Once{}

	logger, err := InitLogger(tmpDir)
	if err != nil {
		t.Fatalf("InitLogger() error = %v", err)
	}
	defer logger.Close()

	if logger == nil {
		t.Fatal("InitLogger() returned nil logger")
	}

	// Verify log file was created
	logPath := filepath.Join(tmpDir, "notification-debug.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("Log file was not created at %s", logPath)
	}

	// Second call should return same logger (singleton)
	logger2, err2 := InitLogger(tmpDir)
	if err2 != nil {
		t.Errorf("Second InitLogger() error = %v", err2)
	}
	if logger2 != logger {
		t.Error("InitLogger() should return same logger instance")
	}
}

func TestInitLogger_EmptyPath(t *testing.T) {
	// Reset defaultLogger for this test
	defaultLogger = nil
	once = sync.Once{}

	logger, err := InitLogger("")
	if err != nil {
		t.Fatalf("InitLogger(\"\") error = %v", err)
	}
	defer logger.Close()
	defer os.Remove("notification-debug.log") // Cleanup

	if logger == nil {
		t.Fatal("InitLogger() should create logger in current directory")
	}
}

func TestGlobalFunctions(t *testing.T) {
	tmpDir := t.TempDir()

	// Reset and initialize default logger
	defaultLogger = nil
	once = sync.Once{}

	logger, err := InitLogger(tmpDir)
	if err != nil {
		t.Fatalf("InitLogger() error = %v", err)
	}
	defer logger.Close()

	// Test global functions
	Debug("debug %s", "test")
	Info("info %d", 123)
	Warn("warn")
	Error("error")

	// Read log file
	logPath := filepath.Join(tmpDir, "notification-debug.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)

	// Verify all log levels are present
	if !strings.Contains(logContent, "[DEBUG]") {
		t.Error("Global Debug() should write to log")
	}
	if !strings.Contains(logContent, "[INFO]") {
		t.Error("Global Info() should write to log")
	}
	if !strings.Contains(logContent, "[WARN]") {
		t.Error("Global Warn() should write to log")
	}
	if !strings.Contains(logContent, "[ERROR]") {
		t.Error("Global Error() should write to log")
	}

	// Test SetPrefix
	SetPrefix("TEST")
	Info("with prefix")

	content, _ = os.ReadFile(logPath)
	if !strings.Contains(string(content), "TEST:") {
		t.Error("Global SetPrefix() should set prefix")
	}

	// Test EnableConsoleOutput
	EnableConsoleOutput()
	if !logger.consoleOutput {
		t.Error("Global EnableConsoleOutput() should enable console output")
	}

	// Test DisableConsoleOutput
	DisableConsoleOutput()
	if logger.consoleOutput {
		t.Error("Global DisableConsoleOutput() should disable console output")
	}

	// Test Close
	err = Close()
	if err != nil {
		t.Errorf("Global Close() error = %v", err)
	}
}

func TestGlobalFunctions_NoDefaultLogger(t *testing.T) {
	// Set defaultLogger to nil
	defaultLogger = nil

	// These should not panic when defaultLogger is nil
	Debug("test")
	Info("test")
	Warn("test")
	Error("test")
	SetPrefix("test")
	EnableConsoleOutput()
	DisableConsoleOutput()

	err := Close()
	if err != nil {
		t.Errorf("Close() with nil defaultLogger should return nil, got %v", err)
	}
}

func TestLogger_Concurrency(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "concurrent.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	// Test concurrent writes
	var wg sync.WaitGroup
	numGoroutines := 10
	messagesPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				logger.Info("goroutine %d message %d", id, j)
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// Read log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	// Count log lines
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	expectedLines := numGoroutines * messagesPerGoroutine

	if len(lines) != expectedLines {
		t.Errorf("Expected %d log lines, got %d", expectedLines, len(lines))
	}

	// Verify all lines contain [INFO]
	for i, line := range lines {
		if !strings.Contains(line, "[INFO]") {
			t.Errorf("Line %d should contain [INFO]: %s", i, line)
		}
	}
}

func TestLogger_AllLevelsFormat(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "format.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	logger.SetPrefix("APP")

	// Log all levels
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")

	// Verify each line has correct format: [timestamp] [level] prefix: message
	for _, line := range lines {
		// Should match: [YYYY-MM-DD HH:MM:SS] [LEVEL] APP: message
		if !strings.Contains(line, "[") {
			t.Errorf("Line should contain timestamp brackets: %s", line)
		}
		if !strings.Contains(line, "APP:") {
			t.Errorf("Line should contain prefix: %s", line)
		}
		if !strings.Contains(line, "message") {
			t.Errorf("Line should contain message: %s", line)
		}
	}
}

func TestLogger_ConsoleOutput_ErrorToStderr(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "console-error.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	// Enable console output
	logger.EnableConsoleOutput()

	// Log ERROR and WARN (should go to stderr)
	logger.Error("error message")
	logger.Warn("warning message")

	// Verify logs were written to file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[ERROR]") {
		t.Error("Log should contain [ERROR]")
	}
	if !strings.Contains(logContent, "[WARN]") {
		t.Error("Log should contain [WARN]")
	}
}

func TestLogger_ConsoleOutput_InfoToStdout(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "console-info.log")

	logger, err := NewLogger(logPath)
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	defer logger.Close()

	// Enable console output
	logger.EnableConsoleOutput()

	// Log INFO and DEBUG (should go to stdout)
	logger.Info("info message")
	logger.Debug("debug message")

	// Verify logs were written to file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "[INFO]") {
		t.Error("Log should contain [INFO]")
	}
	if !strings.Contains(logContent, "[DEBUG]") {
		t.Error("Log should contain [DEBUG]")
	}
}

func TestNewLoggerFromFile(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	logger := NewLoggerFromFile(f)
	logger.SetPrefix("play-sound pid=1")
	logger.Error("failed to play %s", "x.mp3")

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[ERROR] play-sound pid=1: failed to play x.mp3") {
		t.Errorf("unexpected log contents: %q", data)
	}
}

func TestFile(t *testing.T) {
	defaultLogger = nil
	once = sync.Once{}
	if File() != nil {
		t.Fatal("File() should be nil before InitLogger")
	}

	logger, err := InitLogger(t.TempDir())
	if err != nil {
		t.Fatalf("InitLogger() error = %v", err)
	}
	defer logger.Close()
	if File() != logger.file {
		t.Error("File() should return the default logger's file")
	}
}
