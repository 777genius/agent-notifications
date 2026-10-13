//go:build windows

package installruntime

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

// Red condition: an accepted legacy descriptor is mistaken for canonical
// security, so lock preparation leaves inherited or foreign grants in place.
func TestCanonicalPrivateWindowsDescriptor(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	private := fmt.Sprintf("(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)", sid)
	for _, tc := range []struct {
		name string
		sddl string
		want bool
	}{
		{"canonical", "O:" + sid + "D:P" + private, true},
		{"reordered", "O:" + sid + "D:P(A;;FA;;;BA)(A;;FA;;;SY)(A;;FA;;;" + sid + ")", true},
		{"foreign read", "O:" + sid + "D:P" + private + "(A;;GR;;;WD)", false},
		{"foreign write", "O:" + sid + "D:P" + private + "(A;;GW;;;WD)", false},
		{"unprotected", "O:" + sid + "D:" + private, false},
		{"inherited", "O:" + sid + "D:P(A;ID;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"inherit only", "O:" + sid + "D:P(A;OICIIO;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"deny", "O:" + sid + "D:P(D;;GW;;;WD)" + private, false},
		{"wrong owner", "O:WDD:P" + private, false},
		{"missing principal", "O:" + sid + "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)", false},
		{"duplicate principal", "O:" + sid + "D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;SY)", false},
		{"reduced user rights", "O:" + sid + "D:P(A;;GR;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"generic all mask", "O:" + sid + "D:P(A;;GA;;;" + sid + ")(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"null DACL", "O:" + sid + "D:NO_ACCESS_CONTROL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			got, err := canonicalPrivateWindowsDescriptor(sd, user.User.Sid)
			if err != nil || got != tc.want {
				t.Fatalf("canonical descriptor = %v/%v, want %v", got, err, tc.want)
			}
		})
	}
}

// The policy's user and SYSTEM entries may have the same SID. Their identical
// grants are valid; a duplicate grant must not stand in for Administrators.
func TestCanonicalPrivateWindowsDescriptorSystemUser(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sddl string
		want bool
	}{
		{"O:SYD:P(A;;FA;;;SY)(A;;FA;;;SY)(A;;FA;;;BA)", true},
		{"O:SYD:P(A;;FA;;;SY)(A;;FA;;;SY)(A;;FA;;;SY)", false},
	} {
		sd, err := windows.SecurityDescriptorFromString(tc.sddl)
		if err != nil {
			t.Fatal(err)
		}
		got, err := canonicalPrivateWindowsDescriptor(sd, user)
		if err != nil || got != tc.want {
			t.Fatalf("SYSTEM descriptor = %v/%v, want %v", got, err, tc.want)
		}
	}
}
