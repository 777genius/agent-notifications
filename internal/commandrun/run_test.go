package commandrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("TEST_NOTIFICATION_HELPER") == "" {
		return
	}
	switch os.Getenv("TEST_NOTIFICATION_HELPER") {
	case "hang":
		for {
			time.Sleep(time.Second)
		}
	case "overflow":
		fmt.Print(strings.Repeat("x", OutputLimit+1))
		for {
			time.Sleep(time.Second)
		}
	case "descendant":
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "TEST_NOTIFICATION_HELPER=hang")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		fmt.Printf("%d\n", cmd.Process.Pid)
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(0)
}

func TestCancellationAndOutputLimit(t *testing.T) {
	for _, mode := range []string{"hang", "overflow", "descendant"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
			cmd.Env = append(os.Environ(), "TEST_NOTIFICATION_HELPER="+mode)
			started := time.Now()
			output, err := Output(ctx, cmd)
			if err == nil || time.Since(started) > 3*time.Second {
				t.Fatalf("unbounded helper: %v", err)
			}
			if len(output) > OutputLimit {
				t.Fatal("output exceeded bound")
			}
			if mode == "overflow" && !errors.Is(err, ErrOutputLimit) {
				t.Fatalf("overflow: %v", err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("helper was not waited")
			}
		})
	}
}
