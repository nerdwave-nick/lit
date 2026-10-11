package bench

import (
	"errors"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxBody caps ordinary bodies: the live tracker's largest is about 15 KB.
	MaxBody = 15 * 1024
	// BigBody is the size of the few 64 KB bodies (decision 8a79bf42's limit).
	BigBody = 64 * 1024
	// medianBody is the live tracker's median body size.
	medianBody = 870
)

// TextPool is the corpus vocabulary: the lines of this repository's Markdown
// and Go files, so search sees realistic words, identifiers and code.
type TextPool struct{ files [][]string }

// NewTextPool builds a pool from files given as lines.
func NewTextPool(files [][]string) *TextPool { return &TextPool{files: files} }

// LoadTextPool reads every .md and .go file under root, in lexical order,
// skipping hidden directories and testdata.
func LoadTextPool(root string) (*TextPool, error) {
	var files [][]string
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(path); ext != ".md" && ext != ".go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(b) || len(b) == 0 {
			return err
		}
		files = append(files, strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
		return nil
	})
	if e == nil && len(files) == 0 {
		e = errors.New("no Markdown or Go files under " + root)
	}
	return NewTextPool(files), e
}

// Text returns consecutive lines from a random place in the pool, cut to at
// most size bytes on a rune boundary.
func (p *TextPool) Text(rng *rand.Rand, size int) string {
	if size <= 0 {
		return ""
	}
	var b strings.Builder
	f := rng.IntN(len(p.files))
	l := rng.IntN(len(p.files[f]))
	for b.Len() < size {
		b.WriteString(p.files[f][l])
		b.WriteByte('\n')
		if l++; l == len(p.files[f]) {
			f, l = rng.IntN(len(p.files)), 0
		}
	}
	return truncate(b.String(), size)
}

// Title is a one-line issue title of a few words from the pool.
func (p *TextPool) Title(rng *rand.Rand) string {
	for range 20 {
		words := strings.FieldsFunc(p.Text(rng, 120), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
		if len(words) >= 3 {
			return truncate(strings.Join(words[:min(len(words), 8)], " "), 100)
		}
	}
	return "Benchmark issue"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// bodySize draws a body size from a log-normal distribution with the live
// tracker's median (870 B), capped at MaxBody: about 1% of bodies exceed 8 KB.
func bodySize(rng *rand.Rand) int {
	n := int(math.Round(medianBody * math.Exp(0.95*rng.NormFloat64())))
	return min(max(n, 40), MaxBody)
}

// splitLines splits s after each newline; a last line without one is kept.
func splitLines(s string) []string {
	return strings.SplitAfter(strings.TrimSuffix(s, "\n"), "\n")
}

// editBody returns body with a small edit, as an agent's update would make:
// a few lines replaced, inserted or deleted, kept within limit bytes.
func editBody(rng *rand.Rand, pool *TextPool, body string, limit int) string {
	if body == "" {
		return pool.Text(rng, min(200, limit))
	}
	lines := strings.SplitAfter(body, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	fresh := func() []string { return splitLines(pool.Text(rng, 60+rng.IntN(180))) }
	at := rng.IntN(len(lines))
	var next []string
	switch r := rng.Float64(); {
	case r < 0.5: // replace one or two lines
		end := min(at+1+rng.IntN(2), len(lines))
		next = append(append(append(next, lines[:at]...), fresh()...), lines[end:]...)
	case r < 0.8 || len(lines) < 4: // insert
		next = append(append(append(next, lines[:at]...), fresh()...), lines[at:]...)
	default: // delete one or two lines
		end := min(at+1+rng.IntN(2), len(lines))
		next = append(append(next, lines[:at]...), lines[end:]...)
	}
	// Keep every line newline-terminated except possibly the last.
	for i := 0; i < len(next)-1; i++ {
		if !strings.HasSuffix(next[i], "\n") {
			next[i] += "\n"
		}
	}
	out := strings.Join(next, "")
	for len(out) > limit && len(next) > 1 {
		next = next[:len(next)-1]
		out = strings.Join(next, "")
	}
	out = truncate(out, limit)
	for out == body {
		out = truncate(body+pool.Text(rng, 40), limit)
		if len(body) >= limit {
			out = truncate(pool.Text(rng, 40)+body, limit)
		}
	}
	return out
}
