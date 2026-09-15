package main

import "testing"

func TestParseProductArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "flag with value", args: []string{"--product", "codex"}, want: "codex"},
		{name: "equals form", args: []string{"--product=codex"}, want: "codex"},
		{name: "missing value", args: []string{"--product"}, wantErr: true},
		{name: "empty value", args: []string{"--product="}, wantErr: true},
		{name: "duplicate flags", args: []string{"--product", "codex", "--product", "codex"}, wantErr: true},
		{name: "duplicate mixed", args: []string{"--product=codex", "--product", "claude"}, wantErr: true},
		{name: "unknown flag", args: []string{"--product", "codex", "--verbose"}, wantErr: true},
		{name: "stray positional", args: []string{"extra", "--product", "codex"}, wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseProductArgs(tc.args)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: product = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHasProductFlag(t *testing.T) {
	if hasProductFlag([]string{"foo", "bar"}) {
		t.Error("no flag expected")
	}
	if !hasProductFlag([]string{"--product", "codex"}) {
		t.Error("flag expected")
	}
	if !hasProductFlag([]string{"--product=codex"}) {
		t.Error("equals form expected")
	}
}

func TestCodexRouteRequested(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		{name: "legacy claude", argv: []string{"bin", "handle-hook", "Stop"}, want: false},
		{name: "legacy with extra positional", argv: []string{"bin", "handle-hook", "Stop", "junk"}, want: false},
		{name: "codex product", argv: []string{"bin", "handle-hook", "Stop", "--product", "codex"}, want: true},
		{name: "explicit claude product", argv: []string{"bin", "handle-hook", "Stop", "--product", "claude"}, want: false},
		{name: "unknown product", argv: []string{"bin", "handle-hook", "Stop", "--product", "cursor"}, want: true},
		{name: "broken flag", argv: []string{"bin", "handle-hook", "Stop", "--product"}, want: true},
		{name: "other command", argv: []string{"bin", "version"}, want: false},
	}
	for _, tc := range cases {
		if got := codexRouteRequested(tc.argv); got != tc.want {
			t.Errorf("%s: codexRouteRequested = %v, want %v", tc.name, got, tc.want)
		}
	}
}
