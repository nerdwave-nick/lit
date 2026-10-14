package hook

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

func init() {
	register(Event{
		Name:     "statusline",
		HostName: "", // the status line input carries no hook_event_name
		Summary:  "Print the Claude status line, ending with lit's state",
		Description: "Serve Claude Code's status line command. Read the status line JSON on stdin and print one line: an original status line,\n" +
			"then lit's part, joined with \" | \". Without --original the original is a built-in line, \"MODEL EFFORT | DIRECTORY | GIT |\n" +
			"context: N%\". With --original COMMAND it is that command's output: the command runs through sh -c with the same stdin, at most\n" +
			"one second and the width left over in COLUMNS; lit's part is printed alone when it fails.\n" +
			"lit's part is \"🔥 lit | PROJECT | claims:N | STAGE\" (the last part only while a stage is set), \"⚫ not lit\" when the server does\n" +
			"not answer and \"lit: pending\" while the session's association is pending. \"[hooks degraded]\" follows it after hook errors of\n" +
			"the last ten minutes. In a narrow terminal (Claude sets COLUMNS) lit's part shortens first, then the built-in line collapses\n" +
			"step by step. The server is asked every few seconds at most; in between, the line is answered from a cache in the workflow\n" +
			"state directory, within about 300 ms. Only the Claude host has a custom status line.",
		Budget: 1500 * time.Millisecond,
		Handle: statusLine,
	})
}

var (
	// litTimeout bounds lit's own work: reading the cache and, when it is old,
	// one refresh from the server.
	litTimeout = 250 * time.Millisecond
	// gitTimeout bounds `git status` for the built-in line.
	gitTimeout = 250 * time.Millisecond
	// originalTimeout bounds the --original command.
	originalTimeout = time.Second
	// failureWindow is how long a logged hook error keeps the failure indicator
	// up.
	failureWindow = 10 * time.Minute
)

// statusLine prints the status line. Each of its parts degrades on its own (no
// git, no server, a failing original), so it never fails; input that is no
// status line input at all was logged as a failure before it gets here.
func statusLine(c *Call) ([]byte, error) {
	if c.Host.Name != "claude" {
		return nil, nil
	}
	in := parseStatusInput(c.Input.Body)
	cols := terminalColumns()
	git := make(chan *gitInfo, 1)
	if c.Opt.Original == "" {
		go func() { git <- c.readGit(in.Dir) }()
	}
	lit := c.litStatus()
	litOnly := func(level int) string { return strings.Join(lit.segments(level), separator) }
	if c.Opt.Original != "" {
		if original, ok := runOriginal(c.Ctx, c.Opt.Original, c.Input.Body, originalColumns(cols, lit)); ok {
			return []byte(withOriginal(original, lit, cols)), nil
		}
		return []byte(fit(cols, litOnly)), nil
	}
	home, _ := os.UserHomeDir()
	line := defaultLine{Model: in.Model, Effort: in.Effort, Git: <-git, Percent: in.Percent, Lit: lit}
	if in.Dir != "" {
		line.Dir = promptPwd(in.Dir, home)
	}
	return []byte(fit(cols, line.render)), nil
}

func (c *Call) readGit(dir string) *gitInfo {
	ctx, cancel := context.WithTimeout(c.Ctx, gitTimeout)
	defer cancel()
	if g, ok := readGit(ctx, dir); ok {
		return &g
	}
	return nil
}

// terminalColumns is the width Claude Code gave the status line command, or 0
// when it did not.
func terminalColumns() int {
	n, err := strconv.Atoi(os.Getenv("COLUMNS"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
