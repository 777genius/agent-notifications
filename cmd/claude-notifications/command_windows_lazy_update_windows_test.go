//go:build windows

package main

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"
)

// TestPowerShellUnicodeQuoteRoundTrip executes the real Windows PowerShell
// tokenizer; string-only checks cannot establish which glyphs end a literal.
func TestPowerShellUnicodeQuoteRoundTrip(t *testing.T) {
	value := "C:\\Users\\O\u2018Brien\u2019\\Git\u201a\\bin\u201b\\bash.exe"
	command := "[Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(" + powershellSingleQuoted(value) + ")))"
	output, err := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell quoting round trip failed: %v: %s", err, output)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("PowerShell returned invalid Base64 %q: %v", output, err)
	}
	if got := string(decoded); got != value {
		t.Fatalf("PowerShell round trip = %q, want %q", got, value)
	}
}
