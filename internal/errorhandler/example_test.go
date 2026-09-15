package errorhandler_test

import (
	"errors"
	"fmt"

	"github.com/777genius/agent-notifications/internal/errorhandler"
	"github.com/777genius/agent-notifications/internal/logging"
)

// Example demonstrates the error handler usage with console output
func Example() {
	// Initialize logger (in real usage this would be done in main.go)
	if _, err := logging.InitLogger("."); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		return
	}
	defer logging.Close()

	// Initialize error handler with console output enabled
	errorhandler.Init(true, false, true)

	// Example 1: Handle a normal error
	err := errors.New("connection timeout")
	errorhandler.HandleError(err, "Failed to connect to webhook")

	// Example 2: Log info message
	errorhandler.Info("Notification sent successfully")

	// Example 3: Log warning
	errorhandler.Warn("Rate limit at 80%% capacity")

	// Example 4: Protected function execution
	errorhandler.WithRecovery(func() {
		fmt.Println("Safe execution")
	})

	// Output will be in console with [claude-notifications] prefix:
	// [claude-notifications] [timestamp] [ERROR] Failed to connect to webhook: connection timeout
	// [claude-notifications] [timestamp] [INFO] Notification sent successfully
	// [claude-notifications] [timestamp] [WARN] Rate limit at 80% capacity
}

// ExampleSafeGo demonstrates safe goroutine execution
func ExampleSafeGo() {
	errorhandler.Init(true, false, true)

	done := make(chan bool)

	// This goroutine is protected from panics
	errorhandler.SafeGo(func() {
		defer func() { done <- true }()
		fmt.Println("Running in safe goroutine")
	})

	<-done
}
