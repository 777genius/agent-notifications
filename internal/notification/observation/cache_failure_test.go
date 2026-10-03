package observation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Red condition: filesystem failure diagnostics expose the private root/raw
// error, change the public message, or misclassify pre-budget root validation.
func TestClaimFailureDiagnosticsRedactPrivatePath(t *testing.T) {
	c, key := cacheFixture(t)
	c.Root = filepath.Join(c.Root, "PRIVATE_MISSING_ROOT")
	t.Setenv("AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = previous; w.Close(); r.Close() })
	claimed, err := c.Claim(context.Background(), key, 1)
	os.Stderr = previous
	w.Close()
	log, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var failure *ClaimFailure
	if claimed || !errors.As(err, &failure) || err.Error() != "cache_unavailable" || errors.Unwrap(err) != nil {
		t.Fatalf("public error boundary changed: %v/%#v", claimed, err)
	}
	expectedClass := "validation"
	if runtime.GOOS == "windows" {
		expectedClass = "not_found"
	}
	if failure.Phase != "root" || failure.Class != expectedClass || (runtime.GOOS == "windows" && failure.OSCode == 0) || failure.BudgetState != "not_started" || failure.MayHavePublished {
		t.Fatalf("missing-root diagnostics: %#v", failure)
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	all := fmt.Sprintf("%v %+v %#v %s %s", failure, failure, failure, encoded, log)
	if strings.Contains(all, c.Root) || strings.Contains(all, "PRIVATE_") || strings.Contains(all, key) {
		t.Fatalf("diagnostics exposed request/filesystem data: %s", all)
	}
	if !strings.Contains(string(log), "phase=root class="+expectedClass) || !strings.Contains(string(log), "publication_possible=false") {
		t.Fatalf("operator diagnostics lack bounded failure details: %s", log)
	}
}
