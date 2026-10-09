package keys

import (
	"os"
	"strings"
	"testing"
)

// docRows renders the default keymap as the Markdown table rows of docs/tui.md.
func docRows() []string {
	var rows []string
	for _, ctx := range Contexts {
		for _, b := range Default().Bindings(ctx)[:len(vocabulary[ctx])] {
			keys := make([]string, len(b.Keys))
			for i, k := range b.Keys {
				keys[i] = "`" + k + "`"
			}
			rows = append(rows, "| "+string(ctx)+" | `"+string(b.Action)+"` | "+strings.Join(keys, ", ")+" | "+b.Description+" |")
		}
	}
	return rows
}

func TestDocsListTheDefaultKeymap(t *testing.T) {
	b, err := os.ReadFile("../../../docs/tui.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range docRows() {
		if !strings.Contains(string(b), row+"\n") {
			t.Errorf("docs/tui.md lacks the row\n%s", row)
		}
	}
}
