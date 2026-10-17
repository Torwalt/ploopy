// Package lease is the machine's lease file: which subscriptions the author
// lends, on what terms.
package lease

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Kind is the term a lease is lent on.
type Kind string

const (
	Until   Kind = "until"
	Nightly Kind = "nightly"
	Open    Kind = "open"
)

// Lease is one subscription lent on some terms.
type Lease struct {
	Harness string    `json:"harness"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Kind    Kind      `json:"kind"`
	Until   time.Time `json:"until,omitzero"`
	Span    string    `json:"span,omitempty"`
	Keep    int       `json:"keep"`
}

// File is the lease file.
type File struct {
	Leases []Lease `json:"leases"`
}

// Path is where the lease file lives. It is a state directory rather than the
// runtime one, so a reboot keeps "until I reclaim".
func Path() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(dir, "ploopy", "leases.json")
}

// Load reads the lease file. A missing file means nothing is lent.
func Load() (File, error) {
	path := Path()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if body := bytes.TrimSpace(raw); len(body) == 0 || body[0] != '{' {
		return File{}, fmt.Errorf("%s: not a JSON object", path)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := validate(f, path); err != nil {
		return File{}, err
	}
	sort.Slice(f.Leases, func(i, j int) bool { return f.Leases[i].Harness < f.Leases[j].Harness })
	return f, nil
}

// Save replaces the lease file whole, so a reader never sees half of it.
func Save(f File) error {
	f.Leases = append([]Lease{}, f.Leases...)
	sort.Slice(f.Leases, func(i, j int) bool { return f.Leases[i].Harness < f.Leases[j].Harness })
	path := Path()
	if err := validate(f, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Set returns a File with the harness's lease replaced, in harness-name order.
func (f File) Set(l Lease) File {
	out := File{Leases: make([]Lease, 0, len(f.Leases)+1)}
	replaced := false
	for _, old := range f.Leases {
		if old.Harness == l.Harness {
			if !replaced {
				out.Leases = append(out.Leases, l)
				replaced = true
			}
			continue
		}
		out.Leases = append(out.Leases, old)
	}
	if !replaced {
		out.Leases = append(out.Leases, l)
	}
	sort.Slice(out.Leases, func(i, j int) bool { return out.Leases[i].Harness < out.Leases[j].Harness })
	return out
}

// Reclaim returns a File without the harness's lease.
func (f File) Reclaim(harness string) File {
	out := File{Leases: make([]Lease, 0, len(f.Leases))}
	for _, l := range f.Leases {
		if l.Harness != harness {
			out.Leases = append(out.Leases, l)
		}
	}
	return out
}

// Active is the leases that still hold at now. An until lease whose instant
// has passed is absent, though reading leaves it in the file.
func (f File) Active(now time.Time) []Lease {
	var out []Lease
	for _, l := range f.Leases {
		if l.Kind == Until && !l.Until.After(now) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// validate refuses a File that Load would refuse, so Save cannot write one.
func validate(f File, path string) error {
	seen := make(map[string]bool, len(f.Leases))
	for _, l := range f.Leases {
		if l.Harness == "" {
			return fmt.Errorf("%s: a lease has no harness", path)
		}
		if !l.Kind.valid() {
			return fmt.Errorf("%s: lease for %s has kind %q", path, l.Harness, l.Kind)
		}
		if l.Keep < 0 || l.Keep > 100 {
			return fmt.Errorf("%s: lease for %s has keep %d", path, l.Harness, l.Keep)
		}
		if seen[l.Harness] {
			return fmt.Errorf("%s: more than one lease for %s", path, l.Harness)
		}
		seen[l.Harness] = true
	}
	return nil
}

func (k Kind) valid() bool {
	switch k {
	case Until, Nightly, Open:
		return true
	}
	return false
}
