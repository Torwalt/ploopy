// Package guard refuses the git commands an unattended session must never run.
//
// A harness deny list is pattern matching on the command string, and
// `git -C . push` walks straight through it. These rules read the invocation
// instead, so the subcommand is what decides.
package guard

import (
	"strings"
)

// Denied subcommands, whatever flags surround them.
var denied = map[string]string{
	"push":   "pushing is the author's to do",
	"rebase": "rebasing rewrites history the author owns",
	"clean":  "cleaning destroys work the loop cannot recover",
}

// Branches a session must not move onto.
var protected = []string{"master", "main"}

// Check reports whether a shell command must be refused, and why.
func Check(command string) (bool, string) {
	for _, invocation := range gitInvocations(command) {
		sub, args := subcommand(invocation)
		if reason, ok := denied[sub]; ok {
			return true, "git " + sub + ": " + reason
		}
		switch sub {
		case "reset":
			if hasFlag(args, "--hard") {
				return true, "git reset --hard: it destroys work the loop cannot recover"
			}
		case "checkout", "switch":
			for _, arg := range args {
				if !strings.HasPrefix(arg, "-") && isProtected(arg) {
					return true, "git " + sub + " " + arg + ": the loop works on the branch it was started on"
				}
			}
		}
	}
	return false, ""
}

func isProtected(name string) bool {
	for _, branch := range protected {
		if name == branch || name == "origin/"+branch {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

// gitInvocations splits a shell command on the separators that start a new
// one, and keeps the parts that run git.
func gitInvocations(command string) [][]string {
	var out [][]string
	for _, part := range splitShell(command) {
		if len(part) > 0 && base(part[0]) == "git" {
			out = append(out, part[1:])
		}
	}
	return out
}

func base(word string) string {
	if i := strings.LastIndex(word, "/"); i >= 0 {
		return word[i+1:]
	}
	return word
}

// splitShell is a deliberately coarse tokeniser: it drops quotes and splits on
// the operators that chain commands. It decides what to refuse, so erring
// towards more invocations is the safe direction.
func splitShell(command string) [][]string {
	var parts [][]string
	var current []string
	var word strings.Builder
	var quote rune

	flushWord := func() {
		if word.Len() > 0 {
			current = append(current, word.String())
			word.Reset()
		}
	}
	flushPart := func() {
		flushWord()
		if len(current) > 0 {
			parts = append(parts, current)
			current = nil
		}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '\\' && i+1 < len(runes):
			i++
			word.WriteRune(runes[i])
		case c == ';' || c == '\n' || c == '|' || c == '&' || c == '(' || c == ')':
			flushPart()
		case c == ' ' || c == '\t':
			flushWord()
		default:
			word.WriteRune(c)
		}
	}
	flushPart()
	return parts
}

// subcommand is the first word that is not a global flag or its value.
func subcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" || arg == "--namespace":
			i++ // skip its value
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			return arg, args[i+1:]
		}
	}
	return "", nil
}
