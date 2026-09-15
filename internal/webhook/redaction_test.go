package webhook

import (
	"context"
	"errors"
	"fmt"
	"github.com/777genius/agent-notifications/internal/logging"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebhookErrorBoundary(t *testing.T) {
	const secret = "CANARY_URL_header_payload_nested"
	for _, err := range []error{
		&url.Error{Op: "Post", URL: "https://" + secret + "/", Err: errors.New(secret)},
		&HTTPError{StatusCode: 400, Status: secret, Body: secret},
		fmt.Errorf("template %s: %w", secret, errors.New(secret)),
	} {
		got := safeWebhookError(err)
		if got == nil || strings.Contains(got.Error(), secret) || errors.Unwrap(got) != nil {
			t.Fatal("unsafe outward error")
		}
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ErrCircuitOpen, ErrRateLimitExceeded} {
		got := safeWebhookError(fmt.Errorf("%s: %w", secret, sentinel))
		if !errors.Is(got, sentinel) || strings.Contains(got.Error(), secret) {
			t.Fatal("lost safe control-flow semantics")
		}
	}
	if safeWebhookError(nil) != nil {
		t.Fatal("changed success")
	}
}

func TestWebhookTemplateDiagnosticsAreContentFree(t *testing.T) {
	root := t.TempDir()
	logger, err := logging.InitLogger(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Error(err)
		}
	})
	const secret = "CANARY_payload_header_nested_name"
	ctx := &runtimeContext{}
	if _, _, err := ctx.resolveValue(secret, "${{raw_body}}"); err != nil {
		t.Fatal(err)
	}
	ctx.resolveHeaders(map[string]string{secret: "${{raw_body}}"})
	raw, err := os.ReadFile(filepath.Join(root, "notification-debug.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "template value unavailable") {
		t.Fatal("unsafe or absent template diagnostic")
	}
}
