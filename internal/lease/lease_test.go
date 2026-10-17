package lease

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

func writeLeases(t *testing.T, body string) {
	t.Helper()
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func instant(hour, minute int) time.Time {
	return time.Date(2026, 10, 18, hour, minute, 0, 0, time.FixedZone("CEST", 2*60*60))
}

func TestAMissingFileLendsNothing(t *testing.T) {
	stateDir(t)

	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Leases) != 0 || len(f.Active(time.Now())) != 0 {
		t.Fatalf("loaded %+v from a missing file", f)
	}
}

func TestAllThreeKindsRoundTrip(t *testing.T) {
	stateDir(t)
	want := []Lease{
		{Harness: "claude", Model: "sonnet", Effort: "high", Kind: Until, Until: instant(9, 0), Keep: 20},
		{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high", Kind: Open, Keep: 0},
		{Harness: "opencode", Kind: Nightly, Span: "21:00-09:00", Keep: 100},
	}
	if err := Save(File{Leases: want}); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Leases) != len(want) {
		t.Fatalf("loaded %+v", got.Leases)
	}
	for i, w := range want {
		g := got.Leases[i]
		if g.Harness != w.Harness || g.Model != w.Model || g.Effort != w.Effort || g.Kind != w.Kind || g.Span != w.Span || g.Keep != w.Keep {
			t.Fatalf("lease %d: got %+v, want %+v", i, g, w)
		}
		if !g.Until.Equal(w.Until) {
			t.Fatalf("lease %d: got until %v, want %v", i, g.Until, w.Until)
		}
	}
}

func TestTheFileCarriesOnlyWhatAKindUses(t *testing.T) {
	stateDir(t)
	f := File{}.Set(Lease{Harness: "claude", Kind: Until, Until: instant(9, 0), Keep: 20}).
		Set(Lease{Harness: "codex", Kind: Open, Keep: 0}).
		Set(Lease{Harness: "opencode", Kind: Nightly, Span: "21:00-09:00", Keep: 100})
	if err := Save(f); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, key := range []string{`"harness": "claude"`, `"kind": "until"`, `"until": "2026-10-18T09:00:00+02:00"`, `"span": "21:00-09:00"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("%s missing from\n%s", key, body)
		}
	}
	if strings.Count(body, `"until":`) != 1 {
		t.Fatalf("until written for a lease that has none:\n%s", body)
	}
	if strings.Count(body, `"span"`) != 1 {
		t.Fatalf("span written for a lease that has none:\n%s", body)
	}
}

func TestSettingALeaseReplacesItAndReclaimingRemovesIt(t *testing.T) {
	stateDir(t)

	f := File{}.Set(Lease{Harness: "claude", Kind: Open, Keep: 20})
	f = f.Set(Lease{Harness: "claude", Kind: Nightly, Span: "21:00-09:00", Keep: 30})
	if len(f.Leases) != 1 || f.Leases[0].Kind != Nightly || f.Leases[0].Keep != 30 {
		t.Fatalf("replacing left %+v", f.Leases)
	}
	f = f.Set(Lease{Harness: "codex", Kind: Open})
	if err := Save(f); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	loaded = loaded.Reclaim("claude")
	if len(loaded.Leases) != 1 || loaded.Leases[0].Harness != "codex" {
		t.Fatalf("reclaiming left %+v", loaded.Leases)
	}
	if got := loaded.Reclaim("absent"); len(got.Leases) != 1 {
		t.Fatalf("reclaiming an absent harness changed %+v", got.Leases)
	}
}

func TestSaveWritesNameOrder(t *testing.T) {
	stateDir(t)
	f := File{Leases: []Lease{
		{Harness: "codex", Kind: Open},
		{Harness: "claude", Kind: Open},
		{Harness: "astra", Kind: Open},
	}}
	if err := Save(f); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Leases []struct {
			Harness string `json:"harness"`
		} `json:"leases"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	want := []string{"astra", "claude", "codex"}
	if len(onDisk.Leases) != len(want) {
		t.Fatalf("wrote %+v", onDisk.Leases)
	}
	for i, harness := range want {
		if onDisk.Leases[i].Harness != harness {
			t.Fatalf("raw order %+v, want %v\n%s", onDisk.Leases, want, raw)
		}
	}
}

func TestLoadReadsNameOrder(t *testing.T) {
	stateDir(t)
	writeLeases(t, `{"leases":[
		{"harness":"codex","kind":"open"},
		{"harness":"claude","kind":"open"},
		{"harness":"astra","kind":"open"}
	]}`)

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"astra", "claude", "codex"}
	if len(got.Leases) != len(want) {
		t.Fatalf("loaded %+v", got.Leases)
	}
	for i, harness := range want {
		if got.Leases[i].Harness != harness {
			t.Fatalf("order %v, want %v", got.Leases, want)
		}
	}
}

func TestInvalidLeasesAreRefusedByLoadAndSave(t *testing.T) {
	cases := []struct {
		name string
		body string
		file File
	}{
		{
			name: "no harness",
			body: `{"leases":[{"kind":"open"}]}`,
			file: File{Leases: []Lease{{Kind: Open}}},
		},
		{
			name: "unknown kind",
			body: `{"leases":[{"harness":"claude","kind":"soon"}]}`,
			file: File{Leases: []Lease{{Harness: "claude", Kind: "soon"}}},
		},
		{
			name: "keep below zero",
			body: `{"leases":[{"harness":"claude","kind":"open","keep":-1}]}`,
			file: File{Leases: []Lease{{Harness: "claude", Kind: Open, Keep: -1}}},
		},
		{
			name: "keep above one hundred",
			body: `{"leases":[{"harness":"claude","kind":"open","keep":101}]}`,
			file: File{Leases: []Lease{{Harness: "claude", Kind: Open, Keep: 101}}},
		},
		{
			name: "two leases for one harness",
			body: `{"leases":[{"harness":"claude","kind":"open"},{"harness":"claude","kind":"open"}]}`,
			file: File{Leases: []Lease{{Harness: "claude", Kind: Open}, {Harness: "claude", Kind: Open}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("load", func(t *testing.T) {
				stateDir(t)
				writeLeases(t, tc.body)
				if _, err := Load(); err == nil || !strings.Contains(err.Error(), Path()) {
					t.Fatalf("err %v does not name %s", err, Path())
				}
			})
			t.Run("save", func(t *testing.T) {
				stateDir(t)
				before := []byte("{\"leases\":[]}\n")
				path := Path()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, before, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := Save(tc.file); err == nil {
					t.Fatal("Save accepted an invalid lease")
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after, before) {
					t.Fatalf("Save changed the file to\n%s", after)
				}
			})
		})
	}
}

func TestLoadRefusesADocumentThatIsNotAnObject(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"null", "null"},
		{"array", "[]"},
		{"string", `"lent"`},
		{"number", "42"},
		{"bool", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir(t)
			writeLeases(t, tc.body)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), Path()) {
				t.Fatalf("err %v does not name %s", err, Path())
			}
		})
	}
}

func TestSaveWritesAnEmptyLeaseList(t *testing.T) {
	stateDir(t)
	if err := Save(File{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"leases": []`) {
		t.Fatalf("empty file written as\n%s", raw)
	}
}

func TestAnExpiredUntilLeaseIsAbsentButKept(t *testing.T) {
	stateDir(t)
	now := instant(12, 0)
	f := File{Leases: []Lease{
		{Harness: "claude", Kind: Until, Until: now.Add(-time.Minute), Keep: 20},
		{Harness: "codex", Kind: Until, Until: now, Keep: 0},
		{Harness: "gemini", Kind: Until, Until: now.Add(time.Minute), Keep: 0},
		{Harness: "opencode", Kind: Open},
	}}
	if err := Save(f); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Leases) != 4 {
		t.Fatalf("reading dropped a lease: %+v", loaded.Leases)
	}
	active := loaded.Active(now)
	if len(active) != 2 || active[0].Harness != "gemini" || active[1].Harness != "opencode" {
		t.Fatalf("active %+v", active)
	}
}

func TestAnInvalidFileNamesThePath(t *testing.T) {
	stateDir(t)
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err %v does not name %s", err, path)
	}
}

func TestPathFallsBackToTheHomeStateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".local", "state", "ploopy", "leases.json")
	if got := Path(); got != want {
		t.Fatalf("path %s, want %s", got, want)
	}
	if err := Save(File{}.Set(Lease{Harness: "claude", Kind: Open})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(want))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("state directory mode %o", perm)
	}
}
