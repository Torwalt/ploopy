package repo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// Worktree is one checkout of the repository.
type Worktree struct {
	Path   string
	Branch string // empty on a detached head
}

// Worktrees lists every checkout of the repository, the main one first.
// Checkouts git marks bare or prunable are left out.
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var trees []Worktree
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		var tree Worktree
		skip := false
		for _, line := range strings.Split(block, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				tree.Path = value
			case "branch":
				tree.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare", "prunable":
				skip = true
			}
		}
		if tree.Path != "" && !skip {
			trees = append(trees, tree)
		}
	}
	return trees, nil
}

// Ref is a branch and the commit it points at.
type Ref struct {
	Branch string
	Commit string
}

// Unmerged lists the local branches with commits the branch into does not
// have. With into empty, it lists every branch.
func (r *Repo) Unmerged(ctx context.Context, into string) ([]Ref, error) {
	args := []string{"for-each-ref", "--format=%(refname:short) %(objectname)"}
	if into != "" {
		args = append(args, "--no-merged="+into)
	}
	out, err := r.git(ctx, append(args, "refs/heads")...)
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if branch, commit, ok := strings.Cut(line, " "); ok {
			refs = append(refs, Ref{Branch: branch, Commit: commit})
		}
	}
	return refs, nil
}

// FilesAt reads the files directly under dir, as each commit has them, without
// checking anything out. keep picks the file names worth reading. Commits that
// share the directory's tree share one read, so many branches cost little.
// The result maps each commit to its files, keyed by repository path.
func (r *Repo) FilesAt(ctx context.Context, commits []string, dir string, keep func(name string) bool) (map[string]map[string][]byte, error) {
	dir = strings.Trim(path.Clean(dir), "/")
	queries := make([]string, len(commits))
	for i, commit := range commits {
		queries[i] = commit + ":" + dir
	}
	answers, err := r.batch(ctx, "--batch-check", queries)
	if err != nil {
		return nil, err
	}

	treeOf := map[string]string{}
	listed := map[string]map[string]string{} // tree -> repository path -> blob
	var blobs []string
	for i, answer := range answers {
		fields := strings.Fields(answer.header)
		if len(fields) < 2 || fields[1] != "tree" {
			continue
		}
		tree := fields[0]
		treeOf[commits[i]] = tree
		if _, seen := listed[tree]; seen {
			continue
		}
		out, err := r.git(ctx, "ls-tree", tree)
		if err != nil {
			return nil, err
		}
		entries := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			meta, name, ok := strings.Cut(line, "\t")
			parts := strings.Fields(meta)
			if !ok || len(parts) != 3 || parts[1] != "blob" || !keep(name) {
				continue
			}
			entries[dir+"/"+name] = parts[2]
			blobs = append(blobs, parts[2])
		}
		listed[tree] = entries
	}

	contents := map[string][]byte{}
	if len(blobs) > 0 {
		read, err := r.batch(ctx, "--batch", unique(blobs))
		if err != nil {
			return nil, err
		}
		for _, answer := range read {
			if fields := strings.Fields(answer.header); len(fields) > 0 {
				contents[fields[0]] = answer.body
			}
		}
	}

	out := map[string]map[string][]byte{}
	for commit, tree := range treeOf {
		files := map[string][]byte{}
		for repoPath, blob := range listed[tree] {
			files[repoPath] = contents[blob]
		}
		out[commit] = files
	}
	return out, nil
}

type answer struct {
	header string
	body   []byte
}

// batch asks one `git cat-file` process about many objects, in order.
func (r *Repo) batch(ctx context.Context, mode string, queries []string) ([]answer, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", mode)
	cmd.Dir = r.Root
	cmd.Stdin = strings.NewReader(strings.Join(queries, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file %s failed: %s", mode, strings.TrimSpace(stderr.String()))
	}

	reader := bufio.NewReader(bytes.NewReader(stdout))
	answers := make([]answer, 0, len(queries))
	for range queries {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s answered %d of %d", mode, len(answers), len(queries))
		}
		header = strings.TrimSuffix(header, "\n")
		a := answer{header: header}
		fields := strings.Fields(header)
		if mode == "--batch" && len(fields) == 3 && fields[1] != "missing" {
			size, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, err
			}
			a.body = make([]byte, size)
			if _, err := io.ReadFull(reader, a.body); err != nil {
				return nil, err
			}
			if _, err := reader.ReadByte(); err != nil { // the newline after the object
				return nil, err
			}
		}
		answers = append(answers, a)
	}
	return answers, nil
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
