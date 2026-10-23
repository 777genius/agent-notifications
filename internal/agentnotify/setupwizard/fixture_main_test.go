//go:build linux || darwin

package setupwizard

import (
	"fmt"
	"github.com/777genius/agent-notifications/internal/testenv"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := testenv.CleanupFixtures(); err != nil {
		fmt.Fprintln(os.Stderr, "remove TEST fixture compiler cache:", err)
		code = 1
	}
	os.Exit(code)
}
