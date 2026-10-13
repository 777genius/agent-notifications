package config

import (
	"fmt"
	"testing"
)

func TestPermissionFailureSurvivesSelectedPathWrapping(t *testing.T) {
	original := &Error{Code: ConfigPermissionDenied, Path: "profile", permissionFailure: &PermissionFailure{ObjectPath: "profile", Reason: "unrelated_write_grant", TrusteeSID: "S-1-1-0", AccessMask: 64}}
	wrapped := pathErrorAt("selected/config.json", "read", fmt.Errorf("outer: %w", pathError("other-context", original))).(*Error)
	if wrapped.Path != "selected/config.json" || wrapped.Code != ConfigPermissionDenied {
		t.Fatalf("context changed: %+v", wrapped)
	}
	detail := wrapped.PermissionFailure()
	if detail == nil || *detail != *original.PermissionFailure() {
		t.Fatalf("lost rejected object: %+v", detail)
	}
	detail.ObjectPath = "mutated"
	if wrapped.PermissionFailure().ObjectPath != "profile" {
		t.Fatal("caller modified metadata")
	}
	if original.Error() != "ConfigPermissionDenied: path=\"profile\" offset=0" {
		t.Fatal("hook error format changed")
	}
	if (&Error{Code: ConfigInvalid}).PermissionFailure() != nil {
		t.Fatal("fabricated ACL diagnostic")
	}
}
