package commandrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	case "separate":
		_, _ = fmt.Fprint(os.Stderr, "non-fatal diagnostic\n")
		_, _ = fmt.Fprint(os.Stdout, "false\n")
	case "aggregate-overflow":
		var streams sync.WaitGroup
		for _, stream := range []*os.File{os.Stdout, os.Stderr} {
			streams.Add(1)
			go func() {
				defer streams.Done()
				_, _ = stream.Write([]byte(strings.Repeat("x", OutputLimit/2+1)))
			}()
		}
		streams.Wait()
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

func TestOutputSeparatesSuccessfulStderr(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "TEST_NOTIFICATION_HELPER=separate")
	output, err := Output(context.Background(), cmd)
	if err != nil || string(output) != "false\n" {
		t.Fatalf("stdout=%q err=%v", output, err)
	}
}

func TestOutputSharesBudgetAcrossConcurrentStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "TEST_NOTIFICATION_HELPER=aggregate-overflow")
	output, err := Output(ctx, cmd)
	if !errors.Is(err, ErrOutputLimit) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("combined stdout/stderr did not cancel on overflow: %v", err)
	}
	if len(output) > OutputLimit/2+1 || cmd.ProcessState == nil {
		t.Fatalf("stdout=%d bytes, process waited=%t", len(output), cmd.ProcessState != nil)
	}
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
