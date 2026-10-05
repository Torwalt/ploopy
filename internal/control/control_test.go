package control

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func yes() *bool { v := true; return &v }

func register(t *testing.T) *Run {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	run, err := Register(Status{
		Root: "/work/hero-rts", Plan: "docs/plans/DRAFT.md", Agent: "opencode deepseek/deepseek-flash high",
		Settings: Settings{Finish: "none", Peak: "run"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	return run
}

func TestARegisteredRunIsListedWithItsUnit(t *testing.T) {
	run := register(t)
	run.SetUnit("4.2")

	lives, err := List()
	if err != nil || len(lives) != 1 {
		t.Fatalf("listed %v, %v", lives, err)
	}
	live := lives[0]
	if live.PID != os.Getpid() || live.Unit != "4.2" || live.Name() != "DRAFT in hero-rts" {
		t.Fatalf("live %+v", live)
	}
	if live.Wanted.Finish != "none" {
		t.Fatalf("wanted %+v", live.Wanted)
	}
}

func TestAnAdjustmentReachesTheRun(t *testing.T) {
	run := register(t)

	if _, err := Adjust(os.Getpid(), Settings{Finish: "poweroff"}); err != nil {
		t.Fatal(err)
	}
	live, err := Adjust(os.Getpid(), Settings{Push: yes(), Secondary: &Agent{Harness: "claude", Model: "sonnet"}})
	if err != nil {
		t.Fatal(err)
	}

	for _, wanted := range []Settings{live.Wanted, run.Wanted()} {
		if wanted.Finish != "poweroff" || !wanted.Pushes() || wanted.Peak != "run" {
			t.Fatalf("a later change undid an earlier one: %+v", wanted)
		}
		if agent := wanted.SecondaryAgent(); agent == nil || agent.Label() != "claude sonnet" {
			t.Fatalf("secondary %+v", agent)
		}
	}

	if _, err := Adjust(os.Getpid(), Settings{Secondary: &Agent{}}); err != nil {
		t.Fatal(err)
	}
	if run.Wanted().SecondaryAgent() != nil {
		t.Fatal("an empty harness should remove the secondary")
	}
}

func TestTheRunIsToldOfAChange(t *testing.T) {
	run := register(t)
	changed := make(chan Settings, 1)
	stop := make(chan struct{})
	defer close(stop)
	go run.Watch(stop, 10*time.Millisecond, func(s Settings) { changed <- s })

	time.Sleep(30 * time.Millisecond)
	if _, err := Adjust(os.Getpid(), Settings{Stop: yes()}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-changed:
		if !s.Stops() {
			t.Fatalf("changed to %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the run was never told")
	}
}

func TestARunWhoseProcessIsGoneIsClearedAway(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(statusPath(Dir(), dead), Status{PID: dead, Plan: "x.md"}); err != nil {
		t.Fatal(err)
	}

	lives, err := List()
	if err != nil || len(lives) != 0 {
		t.Fatalf("listed %v, %v", lives, err)
	}
	if _, err := os.Stat(statusPath(Dir(), dead)); !os.IsNotExist(err) {
		t.Fatal("the dead run's file was left behind")
	}
	if _, err := Adjust(dead, Settings{Finish: "poweroff"}); err == nil {
		t.Fatal("a dead run was adjusted")
	}
}

func TestClosingWithdrawsTheRun(t *testing.T) {
	run := register(t)
	run.Close()
	if lives, _ := List(); len(lives) != 0 {
		t.Fatalf("listed %v after close", lives)
	}
}
