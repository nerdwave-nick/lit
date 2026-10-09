// Package render turns untrusted Markdown into sanitized, styled terminal
// lines off the UI goroutine.
//
// Render is synchronous and builds its own Glamour renderers for every job,
// so jobs may run concurrently and no renderer is ever shared. Service adds
// a byte-bounded cache and joins concurrent requests for the same key; Slot
// tracks which result a display pane currently wants, so a Bubble Tea model
// can drop stale results (they stay cached). The package does not import
// Bubble Tea: a model wraps Slot.Request's function in a tea.Cmd.
//
// Output is split into top-level blocks. Prose blocks (headings, paragraphs,
// lists, quotes, and any code or table nested inside them) wrap at the job
// width. Top-level code blocks and tables are rendered unwrapped and may be
// wider than the pane; the display scrolls them horizontally on their own.
package render

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"time"

	gansi "charm.land/glamour/v2/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/cells"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/render/sanitize"
)

// Options are render options that change the output, so they are part of
// the cache key.
type Options struct {
	// PreserveNewLines keeps soft line breaks inside paragraphs.
	PreserveNewLines bool
}

// Job describes one render: the raw (unsanitized) content of a record at a
// revision, laid out for an inner width in cells.
type Job struct {
	RecordID string
	Revision int64
	Content  string
	Width    int
	Style    Style
	Options  Options
}

// Key identifies a render result: content hash, revision, inner width,
// style and options, plus the record ID so a pane's wanted key is
// unambiguous. The colour profile is deliberately absent: Bubble Tea
// downsamples colours when it draws.
type Key struct {
	RecordID string
	Revision int64
	Hash     [sha256.Size]byte
	Width    int
	Style    Style
	Options  Options
}

// Key returns the job's cache key.
func (j Job) Key() Key {
	return Key{j.RecordID, j.Revision, sha256.Sum256([]byte(j.Content)), j.Width, j.Style, j.Options}
}

// BlockKind is the layout class of a top-level block.
type BlockKind uint8

const (
	Prose BlockKind = iota // wraps at the pane width; never scrolls horizontally
	Code                   // top-level code block: unwrapped, scrolls horizontally
	Table                  // top-level table: unwrapped, scrolls horizontally
)

// Wraps reports whether blocks of this kind are wrapped to the job width.
func (k BlockKind) Wraps() bool { return k == Prose }

func (k BlockKind) String() string { return [...]string{"prose", "code", "table"}[k] }

// Block is one top-level Markdown block as styled lines, without leading
// or trailing blank lines.
type Block struct {
	Kind  BlockKind
	Lines []string
}

// Width returns the widest line of the block in cells under m.
func (b Block) Width(m cells.Method) int {
	w := 0
	for _, l := range b.Lines {
		w = max(w, cells.Width(m, l))
	}
	return w
}

// Doc is a rendered body. It is immutable once returned and may be shared.
type Doc struct {
	Blocks  []Block
	Elapsed time.Duration // render time, for diagnostics
	size    int64
}

// Line is one display line of a Doc.
type Line struct {
	Text  string
	Block int // index into Doc.Blocks; a separator belongs to the block before it
	Kind  BlockKind
}

// Lines flattens the document for display: blocks in order, separated by
// one blank line.
func (d *Doc) Lines() []Line {
	var out []Line
	for i, b := range d.Blocks {
		if i > 0 {
			out = append(out, Line{Block: i - 1, Kind: Prose})
		}
		for _, l := range b.Lines {
			out = append(out, Line{Text: l, Block: i, Kind: b.Kind})
		}
	}
	return out
}

// Size is the document's cost in the cache, in bytes.
func (d *Doc) Size() int64 { return d.size }

// Render sanitizes and renders one job synchronously with renderers of its
// own. It is safe to call from many goroutines at once.
func Render(j Job) (*Doc, error) {
	start := time.Now()
	cfg := styleConfig(j.Style)
	src := []byte(sanitize.Body(j.Content))
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	root := md.Parser().Parse(text.NewReader(src))
	if cfg.CodeBlock.Theme != "" {
		src = resolveCodeLanguages(root, src)
	}
	prose := newRenderer(cfg, j.Width, j.Options)
	wide := newRenderer(cfg, 0, j.Options)
	margin := strings.Repeat(" ", int(docMargin(cfg)))

	d := &Doc{}
	var buf bytes.Buffer
	for n := root.FirstChild(); n != nil; {
		next := n.NextSibling()
		kind := kindOf(n)
		root.RemoveChild(root, n)
		block := ast.NewDocument()
		block.AppendChild(block, n)
		r := prose
		if !kind.Wraps() {
			r = wide
		}
		buf.Reset()
		if err := r.Render(&buf, src, block); err != nil {
			return nil, err
		}
		lines := trimBlank(strings.Split(sanitize.Rendered(buf.String()), "\n"))
		if !kind.Wraps() {
			for i, l := range lines {
				lines[i] = margin + l // an unwrapped Glamour render drops the document margin
			}
		}
		if len(lines) > 0 {
			d.Blocks = append(d.Blocks, Block{Kind: kind, Lines: lines})
		}
		n = next
	}
	d.size = docSize(d)
	d.Elapsed = time.Since(start)
	return d, nil
}

func newRenderer(cfg gansi.StyleConfig, width int, o Options) renderer.Renderer {
	ar := gansi.NewRenderer(gansi.Options{
		WordWrap:         width,
		PreserveNewLines: o.PreserveNewLines,
		Styles:           cfg,
		ChromaFormatter:  "terminal16m",
	})
	return renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(ar, 1000)))
}

func kindOf(n ast.Node) BlockKind {
	switch n.Kind() {
	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		return Code
	case east.KindTable:
		return Table
	}
	return Prose
}

func docMargin(cfg gansi.StyleConfig) uint {
	if cfg.Document.Margin != nil {
		return *cfg.Document.Margin
	}
	return 0
}

func trimBlank(ls []string) []string {
	blank := func(s string) bool { return strings.TrimSpace(ansi.Strip(s)) == "" }
	for len(ls) > 0 && blank(ls[0]) {
		ls = ls[1:]
	}
	for len(ls) > 0 && blank(ls[len(ls)-1]) {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// docSize estimates the memory a cached Doc holds.
func docSize(d *Doc) int64 {
	n := int64(64)
	for _, b := range d.Blocks {
		n += 48
		for _, l := range b.Lines {
			n += 16 + int64(len(l))
		}
	}
	return n
}
