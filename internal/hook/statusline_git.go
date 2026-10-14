package hook

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// gitInfo is the part of `git status` the status line shows.
type gitInfo struct {
	Branch        string // the branch, or the short object name when detached
	Marks         string // "+" staged, "*" modified, "?" untracked, in that order
	Ahead, Behind int
}

// parseGitStatus reads `git status --porcelain=v2 --branch` output.
func parseGitStatus(porcelain string) (gitInfo, bool) {
	var g gitInfo
	var head, oid string
	var staged, modified, untracked bool
	scanner := bufio.NewScanner(strings.NewReader(porcelain))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.oid "):
			oid = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &g.Ahead, &g.Behind)
		case strings.HasPrefix(line, "? "):
			untracked = true
		case len(line) > 3 && (line[0] == '1' || line[0] == '2' || line[0] == 'u') && line[1] == ' ':
			staged = staged || line[2] != '.'
			modified = modified || line[3] != '.'
		}
	}
	switch {
	case head != "" && head != "(detached)":
		g.Branch = head
	case oid != "" && oid != "(initial)":
		g.Branch = oid[:min(7, len(oid))]
	default:
		return gitInfo{}, false
	}
	for _, mark := range []struct {
		on bool
		c  string
	}{{staged, "+"}, {modified, "*"}, {untracked, "?"}} {
		if mark.on {
			g.Marks += mark.c
		}
	}
	return g, true
}

// label is the branch with its marks and, when counts is set, the commits
// ahead of (↑n) and behind (↓n) the upstream.
func (g gitInfo) label(counts bool) string {
	label := g.Branch + g.Marks
	if counts && g.Ahead > 0 {
		label += " ↑" + strconv.Itoa(g.Ahead)
	}
	if counts && g.Behind > 0 {
		label += " ↓" + strconv.Itoa(g.Behind)
	}
	return label
}

// readGit runs git in dir; a directory outside a repository, a missing git or
// a git that does not answer before ctx ends reports false. Optional locks are
// off so that the status line never makes a user's own git command wait.
func readGit(ctx context.Context, dir string) (gitInfo, bool) {
	if dir == "" {
		return gitInfo{}, false
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "--no-optional-locks", "status", "--porcelain=v2", "--branch").Output()
	if err != nil {
		return gitInfo{}, false
	}
	return parseGitStatus(string(out))
}
