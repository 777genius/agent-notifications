//go:build linux || darwin

package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRatePolicyBounds(t *testing.T) {
	for _, p := range []RatePolicy{{1, 1, 1}, {MaxRate, MaxRate, MaxRate}, {2, 4, 3}, {12, 60, 8}, {}} {
		if _, e := p.Normalize(); e != nil {
			t.Fatal(p, e)
		}
	}
	for _, p := range []RatePolicy{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {-1, 1, 1}, {1, -1, 1}, {1, 1, -1}, {2, 1, 1}, {1, 1, 2}, {MaxRate + 1, MaxRate, 1}, {1, MaxRate + 1, 1}, {1, MaxRate, MaxRate + 1}} {
		t.Run(fmt.Sprint(p), func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			a := admission("R")
			a.Rates = p
			before, e := os.ReadFile(filepath.Join(s.root, "journal.json"))
			requireNoError(t, e)
			r, e := s.Admit(testContext(t), a)
			if !errors.Is(e, ErrInvalid) || r.Fresh {
				t.Fatal(r, e)
			}
			after, e := os.ReadFile(filepath.Join(s.root, "journal.json"))
			requireNoError(t, e)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid policy wrote state")
			}
			a.Rates = RatePolicy{}
			mustAdmit(t, s, a)
		})
	}
}

func TestConfiguredWindows(t *testing.T) {
	for _, tc := range []struct {
		name     string
		p        RatePolicy
		n        int
		step     uint64
		sessions bool
	}{
		{"lower-session", RatePolicy{2, 10, 10}, 2, 3, false},
		{"higher-session", RatePolicy{8, 40, 10}, 8, 3, false},
		{"lower-runtime", RatePolicy{2, 4, 4}, 4, 3, true},
		{"higher-runtime", RatePolicy{40, 40, 40}, 40, 0, true},
		{"lower-burst", RatePolicy{6, 30, 1}, 1, 0, false},
		{"higher-burst", RatePolicy{10, 40, 5}, 5, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := fixture(t, Limits{})
			for i := 0; i <= tc.n; i++ {
				a := admission(fmt.Sprint(i))
				a.Rates = tc.p
				if tc.sessions {
					a.Key.Session = fmt.Sprint(i)
				}
				if i < tc.n {
					mustAdmit(t, s, a)
				} else if r, e := s.Admit(testContext(t), a); !errors.Is(e, ErrRate) || r.Fresh {
					t.Fatal(r, e)
				}
				c.x.Seconds += tc.step
			}
			s = reopen(t, s, c)
			c.x.Seconds += 61
			a := admission("recovered")
			a.Rates = tc.p
			mustAdmit(t, s, a)
		})
	}
}

func TestPolicyChangeReplayAndFrozenCounters(t *testing.T) {
	s, c := fixture(t, Limits{})
	a := admission("original")
	a.Rates = RatePolicy{8, 40, 8}
	first := mustAdmit(t, s, a)
	for i := 0; i < 6; i++ {
		b := admission(fmt.Sprint(i))
		b.Rates = a.Rates
		mustAdmit(t, s, b)
	}
	s = reopen(t, s, c)
	for _, p := range []RatePolicy{{}, {1, 1, 1}, {-1, 0, 0}} {
		a.Rates = p
		a.Decision.Policy = "disabled"
		a.Decision.Target.ID = strings.Repeat("x", 1025)
		a.TrackingID = ""
		r, e := s.Admit(testContext(t), a)
		requireNoError(t, e)
		x, e := json.Marshal(first.Record)
		requireNoError(t, e)
		y, e := json.Marshal(r.Record)
		requireNoError(t, e)
		if r.Fresh || r.ScopedKey != first.ScopedKey || !bytes.Equal(x, y) {
			t.Fatal("replay changed")
		}
		r, e = s.Lookup(testContext(t), a.Key, a.Digest)
		if e != nil || !r.Found {
			t.Fatal(r, e)
		}
	}
	b := admission("next")
	if _, e := s.Admit(testContext(t), b); !errors.Is(e, ErrRate) {
		t.Fatal(e)
	}
	b.Rates = RatePolicy{8, 40, 8}
	mustAdmit(t, s, b)
	for _, sample := range []Sample{{"boot-A", 1, true}, {"boot-B", 999999, true}, {}, {"boot-C", 9999999, true}} {
		c.x = sample
		requireNoError(t, s.Collect(testContext(t)))
		s = reopen(t, s, c)
		b := admission("frozen")
		b.Rates = RatePolicy{8, 40, 8}
		if _, e := s.Admit(testContext(t), b); !errors.Is(e, ErrRate) {
			t.Fatal(sample, e)
		}
	}
}

func TestConfiguredActualProcessQuota(t *testing.T) {
	for _, quota := range []int{1, 5} {
		t.Run(fmt.Sprint(quota), func(t *testing.T) {
			s, _ := fixture(t, Limits{})
			p := RatePolicy{quota, quota, quota}
			for i := 0; i < quota-1; i++ {
				a := admission(fmt.Sprint(i))
				a.Rates = p
				mustAdmit(t, s, a)
			}
			encoded, e := json.Marshal(p)
			requireNoError(t, e)
			cmds := []*exec.Cmd{child(s, "A", "", "", ""), child(s, "B", "", "", "")}
			var out [2]bytes.Buffer
			for i, cmd := range cmds {
				cmd.Env = append(cmd.Env, "JOURNAL_RATES="+string(encoded), "JOURNAL_GATE=go")
				cmd.Stdout = &out[i]
				cmd.Stderr = &out[i]
				requireNoError(t, cmd.Start())
				t.Cleanup(func() {
					if cmd.ProcessState == nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
					}
				})
			}
			ctx := testContext(t)
			for {
				ready, e := filepath.Glob(filepath.Join(s.root, "ready-*"))
				requireNoError(t, e)
				if len(ready) == 2 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			requireNoError(t, os.WriteFile(filepath.Join(s.root, "go"), []byte("go"), 0600))
			for i, cmd := range cmds {
				if e := cmd.Wait(); e != nil {
					t.Fatalf("%v: %s", e, out[i].String())
				}
			}
			all := out[0].String() + out[1].String()
			if strings.Count(all, "FRESH") != 1 || strings.Count(all, "RATE") != 1 || effects(t, s) != 1 {
				t.Fatal(all)
			}
		})
	}
}

// Exercise the maximum structural budget without 10,000 fsync transactions.
// All entries originate from a valid admission template; identities are unique.
func TestExpandedRateIntegrityBudget(t *testing.T) {
	s, _ := fixture(t, Limits{})
	a := admission("R")
	a.Rates = RatePolicy{MaxRate, MaxRate, MaxRate}
	mustAdmit(t, s, a)
	requireNoError(t, s.transaction(testContext(t), func(d *disk) (bool, error) {
		template := d.Records[a.Key.scoped(d.Namespace)]
		d.Records = map[string]Record{}
		d.Events = []event{}
		for i := 0; i < MaxRate; i++ {
			r := template
			r.Attempt = hash("attempt", fmt.Sprint(i))
			d.Records[hash("key", fmt.Sprint(i))] = r
			d.Events = append(d.Events, event{r.Created, r.Session})
		}
		requireNoError(t, d.validate(s))
		d.Events[0].Session = hash("wrong")
		if !errors.Is(d.validate(s), ErrRepair) {
			t.Fatal("mismatched events accepted")
		}
		d.Events[0].Session = template.Session
		d.Events = append(d.Events, event{template.Created, template.Session})
		if !errors.Is(d.validate(s), ErrRepair) {
			t.Fatal("oversized events accepted")
		}
		return false, nil
	}))
}
