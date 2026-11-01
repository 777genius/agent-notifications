package portable

import "testing"

func TestPiWindowsNativeExecutableAdmission(t *testing.T) {
	for _, test := range []struct {
		path, goos string
		allowed    bool
	}{{"/TEST/pi.cmd", "windows", false}, {"/TEST/pi.bat", "windows", false}, {"/TEST/pi", "windows", false}, {"/TEST/pi.exe", "windows", true}, {"/TEST/PI.EXE", "windows", true}, {"/TEST/pi", "linux", true}, {"/TEST/pi", "darwin", true}} {
		t.Run(test.goos+test.path, func(t *testing.T) {
			if got := ValidatePiExecutable(test.path, test.goos) == nil; got != test.allowed {
				t.Fatalf("admitted=%v want %v", got, test.allowed)
			}
		})
	}
}
