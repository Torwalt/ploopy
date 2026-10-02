package harness

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// TailLines of a session's output are kept for adapters that must read an
// outcome out of text, and for the handover fallback.
const TailLines = 400

// Proc is a child process whose output arrives a line at a time. It runs in
// its own process group so a kill takes the whole tree.
type Proc struct {
	lines chan string
	cmd   *exec.Cmd

	once     sync.Once
	mu       sync.Mutex
	tail     []string
	timedOut bool
	done     chan struct{}
}

// StartProc runs argv in dir. Output is merged, streamed on Lines, appended to
// a ring of the last TailLines, and written verbatim to logPath when given.
func StartProc(ctx context.Context, argv []string, dir string, stdin io.Reader, logPath string, timeout time.Duration) (*Proc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout

	var log *os.File
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, err
		}
		if log, err = os.Create(logPath); err != nil {
			return nil, err
		}
	}

	if err := cmd.Start(); err != nil {
		if log != nil {
			log.Close()
		}
		return nil, err
	}

	p := &Proc{lines: make(chan string, 64), cmd: cmd, done: make(chan struct{})}

	timer := time.AfterFunc(timeout, func() {
		p.mu.Lock()
		p.timedOut = true
		p.mu.Unlock()
		p.Kill()
	})
	if timeout <= 0 {
		timer.Stop()
	}

	go func() {
		select {
		case <-ctx.Done():
			p.Kill()
		case <-p.done:
		}
	}()

	go func() {
		defer close(p.lines)
		defer timer.Stop()
		if log != nil {
			defer log.Close()
		}
		reader := bufio.NewReaderSize(out, 64*1024)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				if log != nil {
					io.WriteString(log, line)
				}
				trimmed := trimNewline(line)
				p.remember(trimmed)
				p.lines <- trimmed
			}
			if err != nil {
				return
			}
		}
	}()

	return p, nil
}

func trimNewline(line string) string {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

func (p *Proc) remember(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail = append(p.tail, line)
	if len(p.tail) > TailLines {
		p.tail = p.tail[len(p.tail)-TailLines:]
	}
}

// Lines is the process's merged output.
func (p *Proc) Lines() <-chan string { return p.lines }

// Tail is the last TailLines of output seen so far.
func (p *Proc) Tail() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tail...)
}

// Wait returns the exit status once the output is closed, and whether the
// process was killed for running past its timeout.
func (p *Proc) Wait() (int, bool) {
	for range p.lines { //nolint:revive // drain if the caller did not
	}
	err := p.cmd.Wait()
	close(p.done)

	p.mu.Lock()
	timedOut := p.timedOut
	p.mu.Unlock()

	exit := 0
	if err != nil {
		exit = -1
		var coded *exec.ExitError
		if errors.As(err, &coded) {
			exit = coded.ExitCode()
		}
	}
	if timedOut {
		exit = 124
	}
	return exit, timedOut
}

// Kill ends the process group, politely and then not.
func (p *Proc) Kill() {
	p.once.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		pgid := -p.cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGTERM)
		go func() {
			select {
			case <-p.done:
			case <-time.After(30 * time.Second):
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			}
		}()
	})
}
