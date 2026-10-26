package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RegisterActivity keeps a command visible through follow-ups and final cleanup,
// after its per-plan status has been withdrawn.
func RegisterActivity() (func(), error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".active")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

// AnyRunning checks run announcements without changing another user's files.
// A live PID counts even if its JSON cannot be read: shutdown must fail closed.
func AnyRunning(dirs []string) (bool, error) {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("checking runs in %s: %w", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || (!strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".active")) {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".active"))
			if err == nil && pid > 0 && alive(pid) {
				return true, nil
			}
		}
	}
	return false, nil
}

// MachineDirs finds the standard registries of every user, plus the caller's
// directory when it uses a custom XDG_RUNTIME_DIR or temporary directory.
func MachineDirs(caller string) ([]string, error) {
	dirs := []string{caller}
	for _, pattern := range []string{"/run/user/*/ploopy", filepath.Join(os.TempDir(), "ploopy-*")} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			if strings.HasPrefix(filepath.Base(match), "ploopy-") {
				uid := strings.TrimPrefix(filepath.Base(match), "ploopy-")
				if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
					continue
				}
			}
			dirs = append(dirs, match)
		}
	}
	return dirs, nil
}
