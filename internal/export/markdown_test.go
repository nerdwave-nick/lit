package export

import "testing"

func TestDemoteHeadingsOutsideCodeFences(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"atx levels", "# One\n## Two\n### Three\n#### Four\n##### Five\n###### Six\n",
			"### One\n#### Two\n##### Three\n###### Four\n###### Five\n###### Six\n"},
		{"indented up to three spaces", "   ## Indented\n    # code block\n", "   #### Indented\n    # code block\n"},
		{"not headings", "#hashtag\n#\tTab heading\n\\# escaped\n#\n", "#hashtag\n###\tTab heading\n\\# escaped\n###\n"},
		{"backtick fence", "```sh\n# comment\n```\n# After\n", "```sh\n# comment\n```\n### After\n"},
		{"tilde fence with longer close", "~~~\n# in\n~~~~\n# out\n", "~~~\n# in\n~~~~\n### out\n"},
		{"shorter close does not end the fence", "````\n```\n# in\n````\n", "````\n```\n# in\n````\n"},
		{"other fence character does not end it", "```\n~~~\n# in\n```\n# out", "```\n~~~\n# in\n```\n### out"},
		{"unclosed fence runs to the end", "```\n# in\n", "```\n# in\n"},
		{"setext headings", "Title\n=====\n\nSub\n---\n", "### Title\n\n#### Sub\n"},
		{"thematic break after a blank line", "Text\n\n---\n", "Text\n\n---\n"},
		{"crlf", "# One\r\ntext\r\n", "### One\r\ntext\r\n"},
		{"no final newline", "## Last", "#### Last"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := demoteHeadings(c.in); got != c.want {
				t.Errorf("demoteHeadings(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestRewriteIssueLinks(t *testing.T) {
	const a = "aaaaaaaa-0000-4000-8000-000000000001"
	const missing = "ffffffff-0000-4000-8000-000000000009"
	target := func(id string) (string, bool) {
		if id == a {
			return "../../fix/b/issues/aaaaaaaa-x.md", true
		}
		return "", false
	}
	for _, c := range []struct{ name, in, want string }{
		{"inline", "See [A](lit:issue:" + a + ").", "See [A](../../fix/b/issues/aaaaaaaa-x.md)."},
		{"inline with title", "[A](lit:issue:" + a + " \"t\")", "[A](../../fix/b/issues/aaaaaaaa-x.md \"t\")"},
		{"angle brackets", "[A](<lit:issue:" + a + ">)", "[A](<../../fix/b/issues/aaaaaaaa-x.md>)"},
		{"reference definition", "[a]: lit:issue:" + a + "\n", "[a]: ../../fix/b/issues/aaaaaaaa-x.md\n"},
		{"unknown target stays", "[M](lit:issue:" + missing + ")", "[M](lit:issue:" + missing + ")"},
		{"author relative links stay", "[doc](docs/x.md) [up](../y.md)", "[doc](docs/x.md) [up](../y.md)"},
		{"bare uri stays", "lit:issue:" + a, "lit:issue:" + a},
		{"inside a fence stays", "```\n[A](lit:issue:" + a + ")\n```\n", "```\n[A](lit:issue:" + a + ")\n```\n"},
		{"two on a line", "[A](lit:issue:" + a + ") and [B](lit:issue:" + a + ")",
			"[A](../../fix/b/issues/aaaaaaaa-x.md) and [B](../../fix/b/issues/aaaaaaaa-x.md)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := rewriteIssueLinks(c.in, target); got != c.want {
				t.Errorf("rewriteIssueLinks(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestInlineTextEscapesMarkdown(t *testing.T) {
	if got, want := inline("a [b] *c* _d_ `e` <f> #g \\h"), `a \[b\] \*c\* \_d\_ \`+"`e\\`"+` \<f\> \#g \\h`; got != want {
		t.Fatalf("inline = %q, want %q", got, want)
	}
}

func TestFencedChoosesALongerFence(t *testing.T) {
	if got, want := fenced("diff", []string{"+```go", "+x"}), "````diff\n+```go\n+x\n````\n"; got != want {
		t.Fatalf("fenced = %q, want %q", got, want)
	}
	if got, want := fenced("diff", []string{" a"}), "```diff\n a\n```\n"; got != want {
		t.Fatalf("fenced = %q, want %q", got, want)
	}
}

func TestCodeSpanWrapsBackticks(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`"open"`, "`\"open\"`"},
		{"a`b", "``a`b``"},
		{"`a", "`` `a ``"},
	} {
		if got := codeSpan(c.in); got != c.want {
			t.Errorf("codeSpan(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
