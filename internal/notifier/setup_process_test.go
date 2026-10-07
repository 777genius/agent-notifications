package notifier

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestSetupProcessLiteralCommand(t *testing.T) {
	exe := "/test path/Notifier.app/Contents/MacOS/terminal-notifier-modern"
	args := []string{"--request-permission-json", "--correlation-id", "id", "--nonce", "nonce"}
	cmd := nativeSetupCommand(context.Background(), exe, args)
	if cmd.Path != exe || !reflect.DeepEqual(cmd.Args, append([]string{exe}, args...)) || cmd.Dir != "/" || cmd.WaitDelay != 100*time.Millisecond {
		t.Fatalf("unexpected setup command: %#v", cmd)
	}
	if cmd.Stdin != nil || cmd.Stderr != nil {
		t.Fatal("unexpected inherited input/diagnostic output")
	}
}
