package hook

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const maxOriginalOutput = 64 << 10

// runOriginal runs the status line command the user had before lit, through
// sh -c, with the host's input on stdin and columns as COLUMNS (the host's own
// width when columns is 0). It reports its output without the trailing blank
// space, or false when it failed: a command that cannot start, exits non-zero,
// outlives originalTimeout (it is killed with everything it started) or prints
// nothing.
func runOriginal(ctx context.Context, command string, stdin []byte, columns int) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, originalTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	if columns > 0 {
		cmd.Env = append(cmd.Env, "COLUMNS="+strconv.Itoa(columns))
	}
	out := &capped{limit: maxOriginalOutput}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	ownProcessGroup(cmd)
	if err := cmd.Run(); err != nil {
		return "", false
	}
	text := strings.TrimRight(out.String(), " \t\r\n")
	return text, strings.TrimSpace(text) != ""
}

// capped keeps the first limit bytes written to it and drops the rest.
type capped struct {
	bytes.Buffer
	limit int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.limit - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
