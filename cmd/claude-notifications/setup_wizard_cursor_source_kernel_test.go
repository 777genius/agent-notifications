package main

import (
	"testing"

	"github.com/777genius/agent-notifications/internal/cursorinstall"
)

func TestCursorLinuxSourceQualificationKeepsKernelEvidenceSeparate(t *testing.T) {
	cases := []struct {
		kernel string
		want   string
	}{
		{"6.17.0-1022-azure", cursorP1Qualification},
		{"7.0.0-1017-azure", "cursor-linux-amd64-ubuntu24.04.5-kernel7.0.0-1017-azure-ext4:source-ci-pr497"},
		{"7.0.0-1014-aws", "cursor-linux-amd64-ubuntu24.04.5-kernel7.0.0-1014-aws-ext4:source-ci-aws-20261123"},
		{"7.0.0-1018-aws", "cursor-linux-amd64-ubuntu24.04.5-kernel7.0.0-1018-aws-ext4:qualification-candidate"},
		{"", ""},
		{"7.0.0-1013-aws", ""},
		{"7.0.0-1015-aws", ""},
		{"7.0.0-1014-azure", ""},
		{"7.0.0-1014-aws-extra", ""},
		{"7.0.0-1014-aws\n", ""},
		{"7.0.0-1017-generic", ""},
		{"7.0.0-1018-azure", ""},
	}
	for _, tc := range cases {
		t.Run(tc.kernel, func(t *testing.T) {
			got := cursorLinuxSourceQualification(tc.kernel)
			if got != tc.want {
				t.Fatalf("kernel %q evidence = %q, want %q", tc.kernel, got, tc.want)
			}
			if (got != "") != cursorinstall.SupportsLinuxSourceKernel(tc.kernel) {
				t.Fatal("source admission and evidence identity disagree")
			}
		})
	}
}
