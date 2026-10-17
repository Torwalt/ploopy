// Command fakeagent answers as claude, codex or opencode, depending on the
// name it was run as. It reads a sandbox scenario, keeps its usage windows in
// state.json, and acts on the repository by the role of the prompt.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	switch name {
	case "claude", "codex", "opencode":
	default:
		fmt.Fprintf(os.Stderr, "fakeagent: invoke as claude, codex or opencode, not %q\n", name)
		os.Exit(2)
	}

	if name == "codex" && len(os.Args) > 1 && os.Args[1] == "app-server" {
		if err := appServer(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	all, _, err := scenarios()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sc, ok := all[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "fakeagent: scenario.toml has no [%s] table\n", name)
		os.Exit(1)
	}
	if sc.Outcome == "" {
		sc.Outcome = "done"
	}

	prompt, err := readPrompt(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	state, entry, path, err := openState(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	now := time.Now()
	entry.refresh(now)

	if sc.Outcome == "logged-out" {
		fmt.Fprintln(os.Stderr, "not logged in")
		os.Exit(1)
	}

	if reset, weekly, spent := entry.spent(now); sc.Outcome == "limit" || spent {
		if reset.IsZero() {
			reset = now.Add(sessionWindow)
		}
		if name == "claude" {
			err = speakClaudeLimit(sc, *entry, reset, weekly, os.Args[1:])
		} else if name == "codex" {
			err = speakCodexLimit(reset)
		} else {
			err = speakOpencodeLimit()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		_ = saveState(path, state)
		os.Exit(1)
	}

	before := copyState(entry)
	if sc.Seconds > 0 {
		time.Sleep(time.Duration(sc.Seconds * float64(time.Second)))
	}

	role := role(prompt)
	if role != "" && (sc.Outcome == "done" || sc.Outcome == "red" || sc.Outcome == "dirty") {
		if err := act(role, name, prompt, sc.Outcome); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	entry.consume(sc, time.Now())
	if err := saveState(path, state); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	after := copyState(entry)
	if err := speak(name, role, sc, before, after, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// readPrompt is where each CLI puts the prompt: stdin for claude and codex,
// the last argument for opencode.
func readPrompt(name string) (string, error) {
	if name == "opencode" {
		if len(os.Args) < 2 {
			return "", fmt.Errorf("opencode was run without a prompt")
		}
		return os.Args[len(os.Args)-1], nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func appServer() error {
	all, _, err := scenarios()
	if err != nil {
		return err
	}
	sc, ok := all["codex"]
	if !ok {
		return fmt.Errorf("fakeagent: scenario.toml has no [codex] table")
	}
	_, entry, _, err := openState("codex")
	if err != nil {
		return err
	}
	entry.refresh(time.Now())

	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)
	reply := func(value any) error {
		if err := enc.Encode(value); err != nil {
			return err
		}
		return out.Flush()
	}

	for in.Scan() {
		var message struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &message) != nil {
			continue
		}
		switch message.Method {
		case "initialize":
			if err := reply(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{}}); err != nil {
				return err
			}
		case "initialized":
			if err := reply(map[string]any{
				"jsonrpc": "2.0", "method": "account/updated",
				"params": map[string]any{"authMode": "chatgpt", "planType": "plus"},
			}); err != nil {
				return err
			}
		case "account/rateLimits/read":
			if sc.Outcome == "logged-out" {
				if err := reply(map[string]any{
					"jsonrpc": "2.0", "id": message.ID,
					"error": map[string]any{"message": "not logged in"},
				}); err != nil {
					return err
				}
				continue
			}
			if err := reply(map[string]any{
				"jsonrpc": "2.0", "id": message.ID, "result": limits(entry),
			}); err != nil {
				return err
			}
		}
	}
	return in.Err()
}

func limits(state *HarnessState) map[string]any {
	return map[string]any{
		"rateLimits": map[string]any{
			"limitId":   "codex",
			"primary":   limitWindow(state.Session, sessionWindow),
			"secondary": limitWindow(state.Weekly, weeklyWindow),
		},
	}
}

func limitWindow(window Window, length time.Duration) map[string]any {
	return map[string]any{
		"usedPercent":        window.Used * 100,
		"windowDurationMins": int(length / time.Minute),
		"resetsAt":           unix(window.ResetAt),
	}
}
