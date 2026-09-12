package nativeprotocol

import (
	"strings"
	"testing"
)

func TestSetupCapabilitiesStrict(t *testing.T) {
	const good = `{"schemaVersion":1,"permissionRequestVersions":[1],"backend":"macos.usernotifications"}`
	for _, data := range []string{good, good + "\n"} {
		c, err := DecodeSetupCapabilities([]byte(data))
		if err != nil || c.SchemaVersion != 1 || len(c.PermissionRequestVersions) != 1 || c.PermissionRequestVersions[0] != 1 || c.Backend != "macos.usernotifications" {
			t.Fatalf("supported setup: %+v %v", c, err)
		}
	}
	bad := []string{"", "null", "[]", good + good, good + strings.Repeat(" ", MaxEnvelopeBytes), strings.Replace(good, "macos", string([]byte{255}), 1)}
	for _, versions := range []string{"null", "[]", "[null]", "[0]", "[2]", "[1,2]", "[1,1]", "[1,null]", "[1.0]", `["1"]`, "1", "{}"} {
		bad = append(bad, strings.Replace(good, "[1]", versions, 1))
	}
	for _, version := range []string{"0", "2", "null", "1.0", `"1"`} {
		bad = append(bad, strings.Replace(good, `"schemaVersion":1`, `"schemaVersion":`+version, 1))
	}
	for _, field := range []string{`"schemaVersion":1`, `"permissionRequestVersions":[1]`, `"backend":"macos.usernotifications"`} {
		bad = append(bad, strings.Replace(good, field, field+","+field, 1))
		key := strings.SplitN(field, ":", 2)[0]
		bad = append(bad, strings.Replace(good, field, key+":null", 1))
		bad = append(bad, strings.Replace(good, field, `"unknown":1`, 1))
	}
	bad = append(bad, strings.Replace(good, "macos.usernotifications", "other", 1), strings.Replace(good, "}", `,"actionKinds":["permission"]}`, 1))
	for i, data := range bad {
		if _, err := DecodeSetupCapabilities([]byte(data)); err != ErrInvalidEnvelope {
			t.Fatalf("case %d: expected bounded error, got %v", i, err)
		}
	}
	if _, err := DecodeCapabilities([]byte(good)); err != ErrInvalidEnvelope {
		t.Fatal("setup response accepted as base capabilities")
	}
}

func TestSetupPermissionOutcomesUseExistingEnvelope(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	const nonce = "00000000-0000-4000-8000-000000000002"
	const good = `{"schemaVersion":1,"correlationID":"` + id + `","nonce":"` + nonce + `","backend":"macos.usernotifications","permission":"allowed"}`
	for _, permission := range []string{"allowed", "denied", "undetermined", "unavailable"} {
		data := []byte(strings.Replace(good, "allowed", permission, 1) + "\n")
		p, err := DecodePermission(data, id, nonce)
		if err != nil || p.Permission != permission {
			t.Fatalf("outcome %s: %+v %v", permission, p, err)
		}
		if _, err := DecodeReceipt(data, id, nonce); err != ErrInvalidEnvelope {
			t.Fatal("permission is not a submitted notification receipt")
		}
	}
	bad := []string{"", "null", good + good, good + strings.Repeat(" ", MaxEnvelopeBytes), strings.Replace(good, "allowed", "submitted", 1), strings.Replace(good, `"schemaVersion":1`, `"schemaVersion":2`, 1), strings.Replace(good, "macos.usernotifications", "other", 1)}
	for _, field := range []string{`"schemaVersion":1`, `"correlationID":"` + id + `"`, `"nonce":"` + nonce + `"`, `"backend":"macos.usernotifications"`, `"permission":"allowed"`} {
		key := strings.SplitN(field, ":", 2)[0]
		bad = append(bad, strings.Replace(good, field, key+":null", 1), strings.Replace(good, field, field+","+field, 1), strings.Replace(good, field, `"unknown":1`, 1))
	}
	for i, data := range bad {
		if _, err := DecodePermission([]byte(data), id, nonce); err != ErrInvalidEnvelope {
			t.Fatalf("case %d: expected bounded error, got %v", i, err)
		}
	}
	for _, pair := range [][2]string{{nonce, nonce}, {id, id}, {"bad", nonce}, {id, "bad"}, {"", ""}} {
		if _, err := DecodePermission([]byte(good), pair[0], pair[1]); err != ErrInvalidEnvelope {
			t.Fatal("accepted unmatched or invalid pending identity")
		}
	}
}
