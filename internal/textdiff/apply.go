package textdiff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrApply is wrapped by every error Apply returns.
var ErrApply = errors.New("textdiff: diff does not apply")

// Apply applies a diff made by Diff to base and returns the new text. It is
// strict: hunk headers must be well formed, in order and consistent with
// their lines; context and removed lines must equal base's lines exactly,
// including line endings; and a hunk with fewer than Context lines of
// leading (trailing) context must start (end) at the start (end) of base.
// Any other diff, or another base, is an error wrapping ErrApply.
func Apply(diff []string, base string) (string, error) {
	old := lines(base)
	var out []string
	pos := 0 // the next unconsumed line of old
	for i := 0; i < len(diff); {
		h, ok := parseHeader(diff[i])
		if !ok {
			return "", fail(i, "want a hunk header, have %q", diff[i])
		}
		start := h.oldAt - 1
		if h.oldN == 0 {
			start = h.oldAt
		}
		if start < pos || start+h.oldN > len(old) {
			return "", fail(i, "hunk %q is out of order or outside the base", diff[i])
		}
		out = append(out, old[pos:start]...)
		pos = start
		if want := len(out) + min(h.newN, 1); h.newAt != want {
			return "", fail(i, "hunk %q starts at new line %d, want %d", diff[i], h.newAt, want)
		}
		var err error
		if i, pos, out, err = applyHunk(diff, i+1, h, old, pos, out); err != nil {
			return "", err
		}
	}
	out = append(out, old[pos:]...)
	for k, l := range out[:max(len(out)-1, 0)] {
		if !strings.HasSuffix(l, "\n") {
			return "", fmt.Errorf("%w: new line %d has no line break but is not the last", ErrApply, k+1)
		}
	}
	return strings.Join(out, ""), nil
}

type header struct{ oldAt, oldN, newAt, newN int }

// applyHunk applies the lines of hunk h, which start at diff[i] and at
// old[pos], appending to out. It returns the index after the hunk.
func applyHunk(diff []string, i int, h header, old []string, pos int, out []string) (int, int, []string, error) {
	at := i - 1 // the header
	oldN, newN := 0, 0
	lead, trail, changed := 0, 0, false
	for oldN < h.oldN || newN < h.newN {
		if i == len(diff) {
			return 0, 0, nil, fail(i, "hunk %q has fewer lines than its header counts", diff[at])
		}
		if diff[i] == "" {
			return 0, 0, nil, fail(i, "empty line in hunk %q", diff[at])
		}
		op, text := diff[i][0], diff[i][1:]
		if i+1 < len(diff) && diff[i+1] == NoNewline {
			i++
		} else {
			text += "\n"
		}
		switch {
		case op == ' ' && oldN < h.oldN && newN < h.newN, op == '-' && oldN < h.oldN:
			if old[pos] != text {
				return 0, 0, nil, fail(i, "line %q does not match base line %d %q", text, pos+1, old[pos])
			}
			pos++
			oldN++
			if op == '-' {
				break
			}
			out = append(out, text)
			newN++
		case op == '+' && newN < h.newN:
			out = append(out, text)
			newN++
		default:
			return 0, 0, nil, fail(i, "unexpected line %q in hunk %q", diff[i], diff[at])
		}
		switch {
		case op != ' ':
			changed, trail = true, 0
		case changed:
			trail++
		default:
			lead++
		}
		i++
	}
	switch {
	case !changed:
		return 0, 0, nil, fail(at, "hunk %q changes nothing", diff[at])
	case lead < Context && pos-oldN != 0:
		return 0, 0, nil, fail(at, "hunk %q has %d lines of leading context but does not start the base", diff[at], lead)
	case trail < Context && pos != len(old):
		return 0, 0, nil, fail(at, "hunk %q has %d lines of trailing context but does not end the base", diff[at], trail)
	}
	return i, pos, out, nil
}

// parseHeader parses exactly "@@ -a,b +c,d @@" with unsigned decimal numbers.
func parseHeader(s string) (header, bool) {
	rest, ok := strings.CutPrefix(s, "@@ -")
	if !ok {
		return header{}, false
	}
	rest, ok = strings.CutSuffix(rest, " @@")
	if !ok {
		return header{}, false
	}
	o, n, ok := strings.Cut(rest, " +")
	if !ok {
		return header{}, false
	}
	var h header
	if h.oldAt, h.oldN, ok = parseRange(o); !ok {
		return header{}, false
	}
	if h.newAt, h.newN, ok = parseRange(n); !ok {
		return header{}, false
	}
	return h, true
}

// parseRange parses "a,b", where a is 0 exactly when b is.
func parseRange(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, ",")
	at, okA := number(a)
	n, okB := number(b)
	if !ok || !okA || !okB || (at == 0) != (n == 0) {
		return 0, 0, false
	}
	return at, n, true
}

func number(s string) (int, bool) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func fail(i int, format string, args ...any) error {
	return fmt.Errorf("%w: diff line %d: %s", ErrApply, i+1, fmt.Sprintf(format, args...))
}
