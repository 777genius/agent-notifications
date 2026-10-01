package opencodeevent

import (
	"context"
	"encoding/json"
	uap "github.com/777genius/plugin-kit-ai/sdk/opencode"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func independentSelection(generation string) ClockSelection {
	return ClockSelection{Policy: TimePolicy{ProfileID: "TEST-independent-clock", RawKind: "linux-boottime", NativeReadBoundNS: 103000000, ComparisonBoundNS: 1200000000},
		CalibrationID: "TEST-independent-source", Generation: generation, TranslationBoundNS: 994000000}
}
func literalPrivate(t *testing.T, generation, kind string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "private", generation+"-"+kind+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPrivateLiteralTypedIdentities(t *testing.T) {
	for _, g := range []string{"v1", "v2"} {
		for _, kind := range []string{"turn_idle_verified", "question_asked", "permission_asked", "terminal_error"} {
			t.Run(g+kind, func(t *testing.T) {
				f, err := DecodePrivate(literalPrivate(t, g, kind), independentSelection(g))
				if err != nil {
					t.Fatal(err)
				}
				want := FactIdentity{Kind: kind, Session: "ses_private_sentinel", Execution: "run_native_7", Root: true}
				switch kind {
				case "turn_idle_verified":
					want.Terminal = "msg_completed_6"
				case "question_asked", "permission_asked":
					want.Request = "req_native_4"
				case "terminal_error":
					want.Terminal = "msg_final_error_9"
					binding := NativeTerminalIdentity{Kind: V1FinalMessage, ID: want.Terminal}
					if g == "v2" {
						want.Terminal = "evt_terminal_8"
						binding = NativeTerminalIdentity{Kind: V2TerminalEvent, ID: want.Terminal}
					}
					if f.Provenance.TerminalBinding != binding {
						t.Fatal("lost actual terminal binding")
					}
				}
				if f.Fact != want || f.Provenance.NativeCreatedNS != 1700000000000000000 || f.Provenance.IngressTickNS != 100010000000 || f.Provenance.SpawnTickNS != 101000000000 || f.Provenance.DeadlineTickNS != 121000000000 {
					t.Fatal("original identity/time changed")
				}
			})
		}
	}
}

// Each mutation is a plausible permissive decoder/casing/arithmetic regression.
// The consumer tests below additionally prove rejection precedes filesystem IO.
func invalidPrivateFrames(t *testing.T) map[string][]byte {
	t.Helper()
	base := string(literalPrivate(t, "v1", "terminal_error"))
	frames := map[string][]byte{}
	replacements := map[string][2]string{
		"outerDuplicate":       {`"protocol":1`, `"protocol":1,"protocol":1`},
		"outerUnknown":         {`"protocol":1`, `"protocol":1,"other":true`},
		"caseAlias":            {`"sourceEpoch":`, `"SourceEpoch":`},
		"anchorDuplicate":      {`"monoLoNS":"100000000000"`, `"monoLoNS":"100000000000","monoLoNS":"100000000000"`},
		"anchorUnknown":        {`"rawKind":"linux-boottime"`, `"rawKind":"linux-boottime","kind":"continuous"`},
		"nativeDuplicate":      {`"generation":"v1"`, `"generation":"v1","generation":"v1"`},
		"nativeUnknown":        {`"generation":"v1"`, `"generation":"v1","terminalID":"made_up"`},
		"calibrationUnknown":   {`"sourceLoNS":"1000"`, `"sourceLoNS":"1000","quality":true`},
		"calibrationMissing":   {`"sourceLoNS":"1000",`, ``},
		"calibrationEpoch":     {`"sourceEpoch":"epoch_original_1","sourceLoNS"`, `"sourceEpoch":"another_epoch","sourceLoNS"`},
		"oversizedBracket":     {`"sourceHiNS":"2000"`, `"sourceHiNS":"2000000000"`},
		"nativeBindingMissing": {`,"nativeMessageID":"msg_final_error_9"`, ``},
		"nativeIDControl":      {`"msg_final_error_9"`, `"bad\nID"`},
		"generation":           {`"generation":"v1"`, `"generation":"v2"`},
		"timeBasis":            {`"assistant_created_lower_bound"`, `"assistant_completed"`},
		"null":                 {`"epochStartedTickNS":"99000000000"`, `"epochStartedTickNS":null`},
		"nsNumber":             {`"epochStartedTickNS":"99000000000"`, `"epochStartedTickNS":99000000000`},
		"leadingZero":          {`"epochStartedTickNS":"99000000000"`, `"epochStartedTickNS":"099000000000"`},
		"sign":                 {`"epochStartedTickNS":"99000000000"`, `"epochStartedTickNS":"+99000000000"`},
		"negative":             {`"epochStartedTickNS":"99000000000"`, `"epochStartedTickNS":"-1"`},
		"nsOverflow":           {`"deadlineTickNS":"121000000000"`, `"deadlineTickNS":"9223372036854775808"`},
		"nativeFraction":       {`"nativeTime":1700000000000`, `"nativeTime":1700000000000.1`},
		"nativeExponent":       {`"nativeTime":1700000000000`, `"nativeTime":17e11`},
		"nativeOverflow":       {`"nativeTime":1700000000000`, `"nativeTime":9223372036855`},
		"unsafeNative":         {`"nativeTime":1700000000000`, `"nativeTime":9007199254740992`},
		"noOrigin":             {`"origin":"` + strings.Repeat("11", 32) + `",`, ``},
		"noEpoch":              {`"sourceEpoch":"epoch_original_1",`, ``},
		"noRoot":               {`"rootSession":true`, `"rootSession":false`},
		"policy":               {`"TEST-independent-clock"`, `"sender-policy"`},
		"fence":                {`"fence":"`, `"fence":"0`},
	}
	for name, r := range replacements {
		mutated := strings.Replace(base, r[0], r[1], 1)
		if mutated == base {
			t.Fatalf("invalid independent mutation %s", name)
		}
		frames[name] = []byte(mutated)
	}
	frames["trailing"] = []byte(base + `{}`)
	frames["length"] = []byte(base + strings.Repeat(" ", 4097))
	frames["depth"] = []byte(strings.Replace(base, `"sourceLoNS":"1000"`, `"sourceLoNS":[[[[[[[[[0]]]]]]]]]`, 1))
	frames["legacy"] = []byte(`{"version":1,"kind":"terminal_error","sessionID":"s","turnID":"t","rootSession":true}`)
	return frames
}
func TestPrivateRejectsClosedAndOverflowFrames(t *testing.T) {
	for name, raw := range invalidPrivateFrames(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePrivate(raw, independentSelection("v1")); err == nil {
				t.Fatal("private frame granted authority")
			}
		})
	}
	base := string(literalPrivate(t, "v1", "terminal_error"))
	for _, ms := range []string{"9223372036854", "9223372036855"} {
		raw := []byte(strings.Replace(base, "1700000000000", ms, 1))
		_, err := DecodePrivate(raw, independentSelection("v1"))
		if (ms == "9223372036854") != (err == nil) {
			t.Fatal("checked millisecond conversion edge")
		}
	}
}

type snapshotFunc func() (ClockSnapshot, error)

func (f snapshotFunc) SampleSnapshot() (ClockSnapshot, error) { return f() }
func independentSnapshot() ClockSnapshot {
	return ClockSnapshot{"12345678-1234-1234-1234-123456789abc", "linux-time:4:7", "linux-boottime", 100000000000, 100002000000, 1700000000000000000, 5000000}
}
func TestTrustedClockNativeBoundsAndIndependentFence(t *testing.T) {
	selection := independentSelection("v1")
	// Golden independently derived by Python struct.pack/hashlib at port freeze.
	const fence = "ec07622bb2d8263a9b05ece6ad7ca71cf080471f445f2d99317d8a9f39817489"
	if selection.Policy.Fence("12345678-1234-1234-1234-123456789abc", "linux-time:4:7") != fence {
		t.Fatal("E1 length-prefixed fence drift")
	}
	cases := []struct {
		name   string
		change func(*ClockSnapshot)
	}{
		{"boot", func(s *ClockSnapshot) { s.Boot = strings.ToUpper(s.Boot) }},
		{"zeroBoot", func(s *ClockSnapshot) { s.Boot = "00000000-0000-0000-0000-000000000000" }},
		{"domain", func(s *ClockSnapshot) { s.ClockDomain = "linux-native-time" }},
		{"domainAlias", func(s *ClockSnapshot) { s.ClockDomain = "linux-time:04:7" }},
		{"kind", func(s *ClockSnapshot) { s.ClockKind = "linux-monotonic" }},
		{"width", func(s *ClockSnapshot) { s.MonoHiNs = s.MonoLoNs + 100000001; s.UncertaintyNs = 103000001 }},
		{"regression", func(s *ClockSnapshot) { s.MonoHiNs = s.MonoLoNs - 1 }},
		{"rule", func(s *ClockSnapshot) { s.UncertaintyNs++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := independentSnapshot()
			tc.change(&s)
			c := TrustedClock{Source: snapshotFunc(func() (ClockSnapshot, error) { return s, nil }), Selection: selection}
			if _, err := c.Snapshot(context.Background()); err == nil {
				t.Fatal("unqualified native sample")
			}
		})
	}
	s := independentSnapshot()
	c := TrustedClock{Source: snapshotFunc(func() (ClockSnapshot, error) { return s, nil }), Selection: selection}
	now, err := c.Snapshot(context.Background())
	if err != nil || now.TickNS != 100000000000 || now.WallNS != 1700000000000000000 || now.UncertaintyNS != 1200000000 || now.Kind != "continuous" {
		t.Fatal("thin adapter changed R/T or lower anchor")
	}
	c.Selection.Policy.NativeReadBoundNS = 4999999
	if _, err = c.Snapshot(context.Background()); err == nil {
		t.Fatal("native R exceeded")
	}
	c.Selection = selection
	c.Selection.Policy.ComparisonBoundNS = int64(2*time.Second) + 1
	if _, err = c.Snapshot(context.Background()); err == nil {
		t.Fatal("T upper bound exceeded")
	}
	c.Selection = selection
	c.Selection.TranslationBoundNS++
	if _, err = c.Snapshot(context.Background()); err == nil {
		t.Fatal("incomplete T")
	}
	c.Selection = selection
	c.Source = snapshotFunc(func() (ClockSnapshot, error) { return ClockSnapshot{}, ErrClockUnavailable })
	if _, err = c.Snapshot(context.Background()); err == nil {
		t.Fatal("unavailable native authority")
	}
	for _, os := range []string{"linux", "darwin", "windows", "freebsd"} {
		if _, err := SelectTrustedClock(os, "amd64"); err == nil {
			t.Fatal("real product cell inferred")
		}
	}
}

func TestPrivateLiteralRemainsNeutralWireOne(t *testing.T) {
	// The actual SDK preserves legacy neutral decoding while the private path
	// rejects it. No forked decoder or neutral wire2 is needed.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(literalPrivate(t, "v2", "turn_idle_verified"), &outer); err != nil {
		t.Fatal(err)
	}
	neutral, err := uap.Decode(outer["event"])
	if err != nil || neutral.Version != 1 || neutral.MessageID != "msg_completed_6" || neutral.Provenance == nil || neutral.Provenance.NativeEventID != "evt_terminal_8" {
		t.Fatal("SDK neutral v1 compatibility lost")
	}
	if _, err := DecodePrivate(outer["event"], independentSelection("v2")); err == nil {
		t.Fatal("neutral-only authorized")
	}
}
