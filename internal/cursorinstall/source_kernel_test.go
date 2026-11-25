package cursorinstall

import "testing"

func TestSupportsLinuxSourceKernel(t *testing.T) {
	for _, kernel := range []string{"6.17.0-1022-azure", "7.0.0-1017-azure", "7.0.0-1014-aws"} {
		if !SupportsLinuxSourceKernel(kernel) {
			t.Errorf("source qualification kernel refused: %q", kernel)
		}
	}
	for _, kernel := range []string{"", "7.0.0-1018-azure", "7.0.0-1017-generic", "6.17.0-1023-azure", "7.0.0-1017-azure-extra", "7.0.0-1017-azure\n", "7.0.0-1013-aws", "7.0.0-1015-aws", "7.0.0-1014-azure", "7.0.0-1014-aws-extra", "7.0.0-1014-aws\n"} {
		if SupportsLinuxSourceKernel(kernel) {
			t.Errorf("unqualified kernel accepted: %q", kernel)
		}
	}
}
