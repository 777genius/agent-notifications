package main

import "testing"

// Missing the stock default entry or treating prompt text as a command makes
// these regressions red; accepting attach/wrappers/unknown flags makes denials red.
func TestRuntimeNativeEntryStockV1LocalGrammar(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"defaultTUI", []string{"/TEST/opencode"}, "tui"},
		{"projectTUI", []string{"/TEST/opencode", "./TEST-project"}, "tui"},
		{"unicodeProject", []string{"/TEST/opencode", "/TEST/é😀"}, "tui"},
		{"modelTUI", []string{"/TEST/opencode", "-m", "TEST/model", "--continue", "--no-mdns"}, "tui"},
		{"promptValue", []string{"/TEST/opencode", "--prompt=--attach", "--session", "TEST-session"}, "tui"},
		{"localRun", []string{"/TEST/opencode", "run", "hello", "TEST"}, "run"},
		{"globalLogsRun", []string{"/TEST/opencode", "--print-logs", "run", "hello"}, "run"},
		{"globalLevelRun", []string{"/TEST/opencode", "--log-level", "INFO", "run", "hello"}, "run"},
		{"globalAssignedRun", []string{"/TEST/opencode", "--print-logs=false", "--log-level=DEBUG", "run", "hello"}, "run"},
		{"globalDefaultTUI", []string{"/TEST/opencode", "--print-logs", "--log-level", "WARN"}, "tui"},
		{"globalAttachDenied", []string{"/TEST/opencode", "--print-logs", "run", "--attach=http://TEST.invalid"}, ""},
		{"globalUnknownDenied", []string{"/TEST/opencode", "--TEST-host", "run", "hello"}, ""},
		{"globalForeignDenied", []string{"/TEST/opencode", "--log-level", "INFO", "auth"}, ""},
		{"globalHelpDenied", []string{"/TEST/opencode", "--print-logs", "--help", "run", "hello"}, ""},
		{"globalPureDenied", []string{"/TEST/opencode", "--print-logs", "--pure", "run", "hello"}, ""},
		{"runOptions", []string{"/TEST/opencode", "run", "--dir", "/TEST/project", "--model=TEST/model", "--format", "json", "hello"}, "run"},
		{"runPromptTail", []string{"/TEST/opencode", "run", "--", "--attach", "hello"}, "run"},
		{"runPromptIsCommand", []string{"/TEST/opencode", "run", "session", "delete"}, "run"},
		{"qualifiedServe", []string{"/TEST/opencode", "serve", "--hostname", "127.0.0.1"}, "serve"},
		{"remoteAttach", []string{"/TEST/opencode", "run", "--attach", "http://TEST.invalid"}, ""},
		{"remoteAttachEquals", []string{"/TEST/opencode", "run", "--attach=http://TEST.invalid"}, ""},
		{"unknownFlag", []string{"/TEST/opencode", "--TEST-plugin-host"}, ""},
		{"help", []string{"/TEST/opencode", "--help"}, ""},
		{"version", []string{"/TEST/opencode", "-v"}, ""},
		{"noExternalPlugins", []string{"/TEST/opencode", "--pure"}, ""},
		{"foreignCommand", []string{"/TEST/opencode", "plugin", "install", "TEST"}, ""},
		{"foreignAlias", []string{"/TEST/opencode", "auth"}, ""},
		{"foreignPluginAlias", []string{"/TEST/opencode", "plug"}, ""},
		{"wrapper", []string{"/TEST/opencode", "node", "/TEST/wrapper.mjs"}, ""},
		{"shortBundle", []string{"/TEST/opencode", "-cm", "TEST/model"}, ""},
		{"missingValue", []string{"/TEST/opencode", "--model"}, ""},
		{"valueIsFlag", []string{"/TEST/opencode", "--model", "--attach"}, ""},
		{"emptyOption", []string{"/TEST/opencode", "--=true"}, ""},
		{"invalidBoolean", []string{"/TEST/opencode", "--continue=TEST"}, ""},
		{"emptyProject", []string{"/TEST/opencode", ""}, ""},
		{"nul", []string{"/TEST/opencode", "run", "TEST\x00text"}, ""},
		{"invalidUnicode", []string{"/TEST/opencode", string([]byte{0xff})}, ""},
		{"emptyArgv", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeNativeEntry(tc.args); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRuntimeEntryRequestCannotInventNativeAuthority(t *testing.T) {
	for _, entry := range []string{"serve", "native"} {
		if !runtimeEntryRequest(entry) {
			t.Fatal("closed request rejected")
		}
	}
	for _, entry := range []string{"", "tui", "run", "desktop", "supported"} {
		if runtimeEntryRequest(entry) {
			t.Fatalf("invented request accepted: %q", entry)
		}
	}
	if runtimeEntryMatches("serve", "tui") || runtimeEntryMatches("native", "") ||
		runtimeEntryMatches("supported", "tui") || runtimeEntryMatches("native", "desktop") || !runtimeEntryMatches("native", "tui") ||
		!runtimeEntryMatches("serve", "serve") {
		t.Fatal("request replaced saved native classification")
	}
}
