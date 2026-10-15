package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// budgetTimeout bounds one read, so a hung app server cannot hold a run.
const budgetTimeout = 10 * time.Second

// Budget reads the account's usage windows from `codex app-server`, which
// answers without a model call. An account on an API key has none.
func (*Harness) Budget(ctx context.Context) (*harness.Budget, error) {
	ctx, cancel := context.WithTimeout(ctx, budgetTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	// A child `codex` starts would keep the pipe open past a kill of `codex`
	// alone, and the read waiting on it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		stdin.Close()
		_ = cmd.Cancel()
		_ = cmd.Wait()
	}()
	return readLimits(stdin, stdout)
}

type rpcLine struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type limitsResult struct {
	RateLimits *struct {
		Primary   *window `json:"primary"`
		Secondary *window `json:"secondary"`
	} `json:"rateLimits"`
}

type window struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int     `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

func (w *window) allowance() harness.Allowance {
	if w == nil {
		return harness.Allowance{}
	}
	return harness.Allowance{
		Used:    w.UsedPercent / 100,
		ResetAt: time.Unix(w.ResetsAt, 0),
		Length:  time.Duration(w.WindowDurationMins) * time.Minute,
	}
}

// readLimits speaks just enough JSON-RPC to ask for the rate limits.
func readLimits(in io.Writer, out io.Reader) (*harness.Budget, error) {
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	send := func(message string) error {
		_, err := io.WriteString(in, message+"\n")
		return err
	}
	await := func(id int) (rpcLine, string, error) {
		auth := ""
		for lines.Scan() {
			var line rpcLine
			if json.Unmarshal(lines.Bytes(), &line) != nil {
				continue
			}
			if line.Method == "account/updated" {
				var params struct {
					AuthMode string `json:"authMode"`
				}
				_ = json.Unmarshal(line.Params, &params)
				auth = params.AuthMode
			}
			if line.ID == id && line.Method == "" {
				return line, auth, nil
			}
		}
		if err := lines.Err(); err != nil {
			return rpcLine{}, auth, err
		}
		return rpcLine{}, auth, errors.New("codex app-server closed without an answer")
	}

	if err := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"ploopy","version":"1"}}}`); err != nil {
		return nil, err
	}
	if _, _, err := await(1); err != nil {
		return nil, err
	}
	if err := send(`{"jsonrpc":"2.0","method":"initialized"}`); err != nil {
		return nil, err
	}
	if err := send(`{"jsonrpc":"2.0","id":2,"method":"account/rateLimits/read"}`); err != nil {
		return nil, err
	}
	answer, auth, err := await(2)
	if err != nil {
		return nil, err
	}
	if answer.Error != nil {
		if auth != "" && auth != "chatgpt" {
			return nil, nil
		}
		return nil, fmt.Errorf("codex app-server: %s", answer.Error.Message)
	}
	var result limitsResult
	if err := json.Unmarshal(answer.Result, &result); err != nil {
		return nil, fmt.Errorf("codex app-server rate limits: %w", err)
	}
	if result.RateLimits == nil || result.RateLimits.Primary == nil {
		return nil, nil
	}
	return &harness.Budget{
		At:      time.Now(),
		Session: result.RateLimits.Primary.allowance(),
		Weekly:  result.RateLimits.Secondary.allowance(),
	}, nil
}
