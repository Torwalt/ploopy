package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestARebasedBranchIsPushedOverItsUpstreamWithALease(t *testing.T) {
	root := newRepo(t)
	bare := remote(t, root)
	git(t, root, "branch", "master")
	git(t, root, "switch", "-q", "-c", "feature")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a")
	r := New(root, nil)
	ctx := context.Background()
	if _, err := r.Push(ctx); err != nil {
		t.Fatal(err)
	}

	git(t, root, "switch", "-q", "master")
	write(t, root, "b.txt", "b\n")
	commit(t, root, "b")
	git(t, root, "switch", "-q", "feature")
	if stopped, err := r.Rebase(ctx, "master"); stopped || err != nil {
		t.Fatalf("rebase stopped %v, %v", stopped, err)
	}

	if r.Upstream(ctx, "feature") != "origin" {
		t.Fatal("the pushed branch should have origin as its upstream")
	}
	if _, err := r.PushRewritten(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	if got := strings.TrimSpace(git(t, bare, "rev-parse", "feature")); got != head {
		t.Fatalf("the remote has %s, not the rebased %s", got, head)
	}
	if r.Upstream(ctx, "master") != "" {
		t.Fatal("a branch never pushed has no upstream")
	}
}

func TestAFastForwardRefusesATargetThatMoved(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "feature")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a on work")
	r := New(root, nil)
	if err := r.FastForward(context.Background(), "feature", "work"); !errors.Is(err, ErrMoved) {
		t.Fatalf("want ErrMoved, got %v", err)
	}
}

func TestAFastForwardMovesATargetCheckedOutNowhere(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "target")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a")
	r := New(root, nil)
	if err := r.FastForward(context.Background(), "work", "target"); err != nil {
		t.Fatal(err)
	}
	if git(t, root, "rev-parse", "target") != git(t, root, "rev-parse", "work") {
		t.Fatal("target should be at work's commit")
	}
}
