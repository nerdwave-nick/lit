package render

import (
	"sync"

	gansi "charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	"github.com/alecthomas/chroma/v2"
	cstyles "github.com/alecthomas/chroma/v2/styles"
)

// Style selects the colours of a render.
type Style uint8

const (
	Dark  Style = iota // for dark terminal backgrounds (the fallback)
	Light              // for light terminal backgrounds
	ASCII              // no colour; structure shown with characters (NO_COLOR)
)

func (s Style) String() string { return [...]string{"dark", "light", "ascii"}[s] }

// StyleFor picks the style for a detected or forced background: ASCII when
// colour is disabled (NO_COLOR), otherwise dark or light.
func StyleFor(dark, noColor bool) Style {
	switch {
	case noColor:
		return ASCII
	case dark:
		return Dark
	}
	return Light
}

var (
	stylesOnce sync.Once
	configs    [3]gansi.StyleConfig
)

// styleConfig returns a private copy of Glamour's built-in style whose code
// blocks use a chroma theme registered under a name unique to that style.
// Glamour itself registers every style's code palette under the fixed name
// "charm", so the first style rendered in a process wins for all later ones.
func styleConfig(s Style) gansi.StyleConfig {
	stylesOnce.Do(func() {
		configs[Dark] = withTheme(gstyles.DarkStyleConfig, "lit-tui-dark")
		configs[Light] = withTheme(gstyles.LightStyleConfig, "lit-tui-light")
		ascii := gstyles.ASCIIStyleConfig
		ascii.CodeBlock.Theme, ascii.CodeBlock.Chroma = "", nil
		configs[ASCII] = ascii
	})
	return configs[s]
}

// withTheme registers cfg's code palette as the chroma style name and points
// cfg at it. It runs once, before any render highlights code.
func withTheme(cfg gansi.StyleConfig, name string) gansi.StyleConfig {
	c := cfg.CodeBlock.Chroma
	cstyles.Register(chroma.MustNewStyle(name, chroma.StyleEntries{
		chroma.Text:                chromaEntry(c.Text),
		chroma.Error:               chromaEntry(c.Error),
		chroma.Comment:             chromaEntry(c.Comment),
		chroma.CommentPreproc:      chromaEntry(c.CommentPreproc),
		chroma.Keyword:             chromaEntry(c.Keyword),
		chroma.KeywordReserved:     chromaEntry(c.KeywordReserved),
		chroma.KeywordNamespace:    chromaEntry(c.KeywordNamespace),
		chroma.KeywordType:         chromaEntry(c.KeywordType),
		chroma.Operator:            chromaEntry(c.Operator),
		chroma.Punctuation:         chromaEntry(c.Punctuation),
		chroma.Name:                chromaEntry(c.Name),
		chroma.NameBuiltin:         chromaEntry(c.NameBuiltin),
		chroma.NameTag:             chromaEntry(c.NameTag),
		chroma.NameAttribute:       chromaEntry(c.NameAttribute),
		chroma.NameClass:           chromaEntry(c.NameClass),
		chroma.NameConstant:        chromaEntry(c.NameConstant),
		chroma.NameDecorator:       chromaEntry(c.NameDecorator),
		chroma.NameException:       chromaEntry(c.NameException),
		chroma.NameFunction:        chromaEntry(c.NameFunction),
		chroma.NameOther:           chromaEntry(c.NameOther),
		chroma.Literal:             chromaEntry(c.Literal),
		chroma.LiteralNumber:       chromaEntry(c.LiteralNumber),
		chroma.LiteralDate:         chromaEntry(c.LiteralDate),
		chroma.LiteralString:       chromaEntry(c.LiteralString),
		chroma.LiteralStringEscape: chromaEntry(c.LiteralStringEscape),
		chroma.GenericDeleted:      chromaEntry(c.GenericDeleted),
		chroma.GenericEmph:         chromaEntry(c.GenericEmph),
		chroma.GenericInserted:     chromaEntry(c.GenericInserted),
		chroma.GenericStrong:       chromaEntry(c.GenericStrong),
		chroma.GenericSubheading:   chromaEntry(c.GenericSubheading),
		chroma.Background:          chromaEntry(c.Background),
	}))
	cfg.CodeBlock.Theme, cfg.CodeBlock.Chroma = name, nil
	return cfg
}

// chromaEntry mirrors Glamour's private chromaStyle conversion.
func chromaEntry(p gansi.StylePrimitive) string {
	var parts []string
	if p.Color != nil {
		parts = append(parts, *p.Color)
	}
	if p.BackgroundColor != nil {
		parts = append(parts, "bg:"+*p.BackgroundColor)
	}
	for _, f := range []struct {
		on   *bool
		name string
	}{{p.Italic, "italic"}, {p.Bold, "bold"}, {p.Underline, "underline"}} {
		if f.on != nil && *f.on {
			parts = append(parts, f.name)
		}
	}
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}

// Margin is the document margin in cells that every rendered line of style s
// starts with: Glamour's prose margin, which Render also prepends to the
// lines of unwrapped blocks. A viewport keeps it fixed while a block scrolls.
func (s Style) Margin() int { return int(docMargin(styleConfig(s))) }
