package render

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// lexerMemoLimit bounds the memo, since language names come from content.
const lexerMemoLimit = 512

var (
	lexerMu   sync.Mutex
	lexerMemo = map[string]string{} // language name -> chroma lexer name, "" if unknown
)

// lexerByName resolves a code block's language name to a chroma lexer name,
// memoised: a miss in chroma's registry scans every lexer's filename
// patterns and costs milliseconds.
func lexerByName(lang string) string {
	lexerMu.Lock()
	name, ok := lexerMemo[lang]
	lexerMu.Unlock()
	if ok {
		return name
	}
	if l := lexers.Get(lang); l != nil {
		name = l.Config().Name
	}
	lexerMu.Lock()
	if len(lexerMemo) < lexerMemoLimit {
		lexerMemo[lang] = name
	}
	lexerMu.Unlock()
	return name
}

// resolveLexer returns the chroma lexer for a code block: the named
// language when chroma knows it, otherwise chroma's guess from the content,
// otherwise plain text.
func resolveLexer(lang, code string) string {
	if lang != "" {
		if name := lexerByName(lang); name != "" {
			return name
		}
	}
	if l := lexers.Analyse(code); l != nil {
		return l.Config().Name
	}
	return "plaintext"
}

// resolveCodeLanguages replaces every code block, at any depth, with a
// fenced block naming its resolved chroma lexer, so Glamour's highlighter
// finds the lexer with one exact-name lookup. The names are appended to a
// copy of src that the returned buffer holds; existing offsets stay valid.
func resolveCodeLanguages(root ast.Node, src []byte) []byte {
	var blocks []ast.Node
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && (n.Kind() == ast.KindFencedCodeBlock || n.Kind() == ast.KindCodeBlock) {
			blocks = append(blocks, n)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if len(blocks) == 0 {
		return src
	}
	out := append(src[:len(src):len(src)], '\n')
	for _, n := range blocks {
		lang := ""
		if f, ok := n.(*ast.FencedCodeBlock); ok && f.Info != nil {
			lang, _, _ = strings.Cut(strings.TrimSpace(string(f.Info.Segment.Value(src))), " ")
		}
		var code strings.Builder
		for i := 0; i < n.Lines().Len(); i++ {
			seg := n.Lines().At(i)
			code.Write(seg.Value(src))
		}
		name := resolveLexer(lang, code.String())
		start := len(out)
		out = append(out, name...)
		fenced := ast.NewFencedCodeBlock(ast.NewTextSegment(text.NewSegment(start, len(out))))
		fenced.SetLines(n.Lines())
		n.Parent().ReplaceChild(n.Parent(), n, fenced)
	}
	return out
}
