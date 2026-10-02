package guard

import "testing"

func TestTheObviousSpellingsAreRefused(t *testing.T) {
	for _, command := range []string{
		"git push",
		"git push --force origin HEAD",
		"git rebase -i HEAD~3",
		"git clean -fd",
		"git reset --hard HEAD",
		"git checkout master",
		"git switch main",
	} {
		if denied, _ := Check(command); !denied {
			t.Fatalf("%q was allowed", command)
		}
	}
}

// A deny list of command-line patterns cannot see past a global flag; reading
// the invocation can.
func TestAGlobalFlagDoesNotHideTheSubcommand(t *testing.T) {
	for _, command := range []string{
		"git -C . push",
		"git -c user.name=x push origin main",
		"git --git-dir=.git push",
		"/usr/bin/git push",
	} {
		if denied, _ := Check(command); !denied {
			t.Fatalf("%q walked through the guard", command)
		}
	}
}

func TestARefusedCommandLaterInAChainIsFound(t *testing.T) {
	for _, command := range []string{
		"go test ./... && git push",
		"git add -A; git commit -m x; git push",
		"true | git push",
	} {
		if denied, _ := Check(command); !denied {
			t.Fatalf("%q walked through the guard", command)
		}
	}
}

func TestTheWorkASessionMustDoIsAllowed(t *testing.T) {
	for _, command := range []string{
		"git add -A",
		"git commit -m 'add the thing'",
		"git status --porcelain",
		"git log --oneline -5",
		"git reset HEAD~1",
		"git checkout -b feature/x",
		"go test ./...",
		"rg pushed",
		"echo 'git push' >> notes.md",
	} {
		if denied, reason := Check(command); denied {
			t.Fatalf("%q was refused: %s", command, reason)
		}
	}
}

func TestARefusalSaysWhy(t *testing.T) {
	denied, reason := Check("git push")
	if !denied || reason == "" {
		t.Fatalf("denied=%v reason=%q", denied, reason)
	}
}
