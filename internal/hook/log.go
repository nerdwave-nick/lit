package hook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// The hook log is <workflow state>/hooks/hook.log: one JSON object per line,
// rotated to hook.log.1 when it grows past logLimit. Errors in it are hook
// failures, which RecentFailures lists for the status line; warnings explain
// conditions that are not failures, such as a server that is down.
const (
	logName  = "hook.log"
	logLimit = 256 << 10
)

// Failure is one logged hook error.
type Failure struct {
	Time      time.Time
	Host      string
	Event     string
	RuntimeID string
	Message   string
}

type entry struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Host      string    `json:"host"`
	Event     string    `json:"event"`
	RuntimeID string    `json:"runtime_id,omitempty"`
	Message   string    `json:"message"`
}

// Log writes the hook log for one served event.
type Log struct {
	Host, Event, Runtime string
	// fallback receives entries that cannot be written; nil drops them.
	fallback io.Writer
	now      func() time.Time
	limit    int64
}

func hooksDir() (string, error) {
	state, err := workflowsession.StateDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "hooks")
	return dir, os.MkdirAll(dir, 0o700)
}

// Path is the log file, whether or not it exists.
func (l *Log) Path() string {
	state, err := workflowsession.StateDir()
	if err != nil {
		return logName
	}
	return filepath.Join(state, "hooks", logName)
}

// Error records a hook failure.
func (l *Log) Error(msg string) { l.write("error", msg) }

// Warn records a condition that is not a failure.
func (l *Log) Warn(msg string) { l.write("warn", msg) }

func (l *Log) write(level, msg string) {
	msg = oneLine(msg)
	line, _ := json.Marshal(entry{Time: l.now().UTC(), Level: level, Host: l.Host, Event: l.Event, RuntimeID: l.Runtime, Message: msg})
	if err := l.append(append(line, '\n')); err != nil && l.fallback != nil {
		fmt.Fprintf(l.fallback, "lit hook: %s: %s (the hook log is unwritable: %v)\n", level, msg, err)
	}
}

func (l *Log) append(line []byte) error {
	dir, err := hooksDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, logName)
	limit := l.limit
	if limit == 0 {
		limit = logLimit
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= limit {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// RecentFailures lists the errors logged at or after since, oldest first.
// The status line reads it to flag failing hooks; it never fails for a
// missing log.
func RecentFailures(since time.Time) ([]Failure, error) {
	state, err := workflowsession.StateDir()
	if err != nil {
		return nil, err
	}
	var failures []Failure
	for _, name := range []string{logName + ".1", logName} {
		f, err := os.Open(filepath.Join(state, "hooks", name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return failures, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			var e entry
			if json.Unmarshal(scanner.Bytes(), &e) == nil && e.Level == "error" && !e.Time.Before(since) {
				failures = append(failures, Failure{e.Time, e.Host, e.Event, e.RuntimeID, e.Message})
			}
		}
		f.Close()
	}
	return failures, nil
}

// oneLine folds whitespace so that a message, however it was produced, stays
// one line, and bounds its length.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 400 {
		s = string(r[:400]) + "..."
	}
	return s
}
