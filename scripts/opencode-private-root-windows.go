//go:build windows

// This E2E fixture makes the disposable sandbox inherit the installer's private ACL.
package main

import (
	"fmt"
	"os"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "one disposable sandbox root required")
		os.Exit(2)
	}
	if err := installruntime.RestrictPrivatePath(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
