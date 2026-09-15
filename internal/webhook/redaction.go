package webhook

import (
	"context"
	"errors"
)

// safeWebhookError is the outward boundary after retry/status classification.
// URL errors, response bodies, template names and transport errors may contain
// credentials. Retain only known control-flow sentinels, never the raw cause.
func safeWebhookError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ErrCircuitOpen, ErrRateLimitExceeded} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return errors.New("webhook delivery failed")
}
