package hook

import (
	"strings"
	"testing"
)

func TestPromptPwdShortensLikeFish(t *testing.T) {
	for _, c := range []struct{ dir, home, want string }{
		{"/home/nerdwave/repos/lit", "/home/nerdwave", "~/r/lit"},
		{"/home/nerdwave", "/home/nerdwave", "~"},
		{"/home/nerdwave/", "/home/nerdwave", "~"},
		{"/home/nerdwave/.config/fish/conf.d", "/home/nerdwave", "~/.c/f/conf.d"},
		{"/usr/local/bin", "/home/nerdwave", "/u/l/bin"},
		{"/home/nerdwavefoo/x", "/home/nerdwave", "/h/n/x"},
		{"/", "/home/nerdwave", "/"},
		{"/tmp", "", "/tmp"},
		{"/home/nerdwave/ünï/çode", "/home/nerdwave", "~/ü/çode"},
		{"relative/path", "", "r/path"},
	} {
		if got := promptPwd(c.dir, c.home); got != c.want {
			t.Errorf("promptPwd(%q, %q) = %q, want %q", c.dir, c.home, got, c.want)
		}
	}
}

func TestDisplayWidthCountsColumns(t *testing.T) {
	for _, c := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"🔥 lit", 6},
		{"⚫ not lit", 10},
		{"↑1 ↓2", 5},
		{"日本", 4},
		{"e\u0301", 1},
		{"a\u200bb", 2},
		{"\x1b[32mgreen\x1b[0m", 5},
		{"\x1b]8;;https://example.org\x1b\\link\x1b]8;;\x1b\\", 4},
		{"\x1b]8;;https://example.org\alink\x1b]8;;\a", 4},
		{"tab\tstop", 7},
	} {
		if got := displayWidth(c.text); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestParseGitStatus(t *testing.T) {
	const head = "# branch.oid 0123456789abcdef0123456789abcdef01234567\n# branch.head main\n"
	for _, c := range []struct {
		name, porcelain string
		want            gitInfo
		counts, plain   string
	}{
		{"clean", head, gitInfo{Branch: "main"}, "main", "main"},
		{"untracked", head + "? new.txt\n", gitInfo{Branch: "main", Marks: "?"}, "main?", "main?"},
		{"modified", head + "1 .M N... 100644 100644 100644 a b file.go\n", gitInfo{Branch: "main", Marks: "*"}, "main*", "main*"},
		{"staged", head + "1 M. N... 100644 100644 100644 a b file.go\n", gitInfo{Branch: "main", Marks: "+"}, "main+", "main+"},
		{"all marks in order", head + "? x\n1 MM N... 100644 100644 100644 a b f\n", gitInfo{Branch: "main", Marks: "+*?"}, "main+*?", "main+*?"},
		{"renamed", head + "2 R. N... 100644 100644 100644 a b R100 new\told\n", gitInfo{Branch: "main", Marks: "+"}, "main+", "main+"},
		{"unmerged", head + "u UU N... 100644 100644 100644 100644 a b c f\n", gitInfo{Branch: "main", Marks: "+*"}, "main+*", "main+*"},
		{"ahead and behind", head + "# branch.upstream origin/main\n# branch.ab +3 -2\n", gitInfo{Branch: "main", Ahead: 3, Behind: 2}, "main ↑3 ↓2", "main"},
		{"only ahead", head + "# branch.ab +1 -0\n", gitInfo{Branch: "main", Ahead: 1}, "main ↑1", "main"},
		{"only behind", head + "# branch.ab +0 -4\n", gitInfo{Branch: "main", Behind: 4}, "main ↓4", "main"},
		{"detached", "# branch.oid 0123456789abcdef0123456789abcdef01234567\n# branch.head (detached)\n", gitInfo{Branch: "0123456"}, "0123456", "0123456"},
		{"unborn", "# branch.oid (initial)\n# branch.head main\n", gitInfo{Branch: "main"}, "main", "main"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseGitStatus(c.porcelain)
			if !ok || got != c.want {
				t.Fatalf("parseGitStatus = %+v %v, want %+v", got, ok, c.want)
			}
			if got.label(true) != c.counts || got.label(false) != c.plain {
				t.Errorf("label = %q / %q, want %q / %q", got.label(true), got.label(false), c.counts, c.plain)
			}
		})
	}
	if _, ok := parseGitStatus(""); ok {
		t.Error("no branch line must not be a repository")
	}
}

// sample is a session with everything the line can show.
func sample(l litState) defaultLine {
	return defaultLine{Model: "Opus 5.5", Effort: "high", Dir: "~/r/lit", Git: &gitInfo{Branch: "main", Marks: "+*?", Ahead: 1, Behind: 2}, Percent: 23, Lit: l}
}

var working = litState{Kind: kindReady, Project: "feat/tracker", Claims: 3, Stage: "impl"}

// collapseOrder is the sample line at every level, from the full form down.
var collapseOrder = []string{
	"Opus 5.5 high | ~/r/lit | main+*? ↑1 ↓2 | context: 23% | 🔥 lit | feat/tracker | claims:3 | impl",
	"Opus 5.5 high | ~/r/lit | main+*? | context: 23% | 🔥 lit | feat/tracker | claims:3 | impl",
	"Opus 5.5 high | ~/r/lit | main+*? | context: 23% | 🔥 lit | feat/tracker | claims:3 | impl",
	"Opus 5.5 | ~/r/lit | main+*? | context: 23% | 🔥 lit | feat/tracker | claims:3 | impl",
	"Opus 5.5 | ~/r/lit | main+*? | 23% | 🔥 lit | feat/tracker | claims:3 | impl",
	"Opus 5.5 | ~/r/lit | main+*? | 23% | 🔥 feat/tracker | c:3 | impl",
	"Opus 5.5 | lit | main+*? | 23% | 🔥 feat/tracker | c:3 | impl",
	"Opus 5.5 | lit | 23% | 🔥 feat/tracker | c:3 | impl",
	"lit | 23% | 🔥 feat/tracker | c:3 | impl",
	"23% | 🔥 feat/tracker | c:3 | impl",
	"23% | 🔥 c:3 | impl",
}

func TestDefaultLineCollapsesInOrder(t *testing.T) {
	if len(collapseOrder) != maxLevel+1 {
		t.Fatalf("the order has %d steps, want %d", len(collapseOrder), maxLevel+1)
	}
	for level, line := range collapseOrder {
		if got := sample(working).render(level); got != line {
			t.Errorf("level %d:\n got %q\nwant %q", level, got, line)
		}
	}
}

func TestDefaultLineShortensTheNoProjectWordingBeforeDroppingEffort(t *testing.T) {
	none := litState{Kind: kindReady}
	for level, want := range map[int]string{
		0: "Opus 5.5 high | ~/r/lit | main+*? ↑1 ↓2 | context: 23% | 🔥 lit | no project selected | claims:0",
		1: "Opus 5.5 high | ~/r/lit | main+*? | context: 23% | 🔥 lit | no project selected | claims:0",
		2: "Opus 5.5 high | ~/r/lit | main+*? | context: 23% | 🔥 lit | no project | claims:0",
		3: "Opus 5.5 | ~/r/lit | main+*? | context: 23% | 🔥 lit | no project | claims:0",
		5: "Opus 5.5 | ~/r/lit | main+*? | 23% | 🔥 no project | c:0",
		9: "23% | 🔥 no project | c:0",
	} {
		if got := sample(none).render(level); got != want {
			t.Errorf("level %d:\n got %q\nwant %q", level, got, want)
		}
	}
}

func TestDefaultLineLeavesOutWhatIsMissing(t *testing.T) {
	for name, c := range map[string]struct {
		line defaultLine
		want string
	}{
		"no git":     {defaultLine{Model: "Opus", Effort: "high", Dir: "/t/x", Percent: 0, Lit: litState{Kind: kindOffline}}, "Opus high | /t/x | context: 0% | ⚫ not lit"},
		"no effort":  {defaultLine{Model: "Haiku", Dir: "~", Percent: 7, Lit: litState{Kind: kindOffline}}, "Haiku | ~ | context: 7% | ⚫ not lit"},
		"no model":   {defaultLine{Effort: "high", Dir: "~", Percent: 7, Lit: litState{Kind: kindOffline}}, "high | ~ | context: 7% | ⚫ not lit"},
		"no dir":     {defaultLine{Model: "Opus", Percent: 7, Lit: litState{Kind: kindOffline}}, "Opus | context: 7% | ⚫ not lit"},
		"nothing":    {defaultLine{Lit: litState{Kind: kindOffline}}, "context: 0% | ⚫ not lit"},
		"root dir":   {defaultLine{Dir: "/", Lit: litState{Kind: kindPending}}, "/ | context: 0% | lit: pending"},
		"no session": {defaultLine{Model: "Opus", Lit: litState{Kind: kindNoSession}}, "Opus | context: 0% | 🔥 lit | no session"},
	} {
		if got := c.line.render(0); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
	if got := (defaultLine{Dir: "/", Lit: litState{Kind: kindOffline}}).render(6); got != "/ | 0% | ⚫" {
		t.Errorf("the root stays / when shortened: %q", got)
	}
}

func TestLitPartInEveryState(t *testing.T) {
	for name, c := range map[string]struct {
		state litState
		want  [3]string // full, compact (level 5), smallest (level 10)
	}{
		"ready":            {working, [3]string{"🔥 lit | feat/tracker | claims:3 | impl", "🔥 feat/tracker | c:3 | impl", "🔥 c:3 | impl"}},
		"no stage":         {litState{Kind: kindReady, Project: "feat/x", Claims: 0}, [3]string{"🔥 lit | feat/x | claims:0", "🔥 feat/x | c:0", "🔥 c:0"}},
		"no project":       {litState{Kind: kindReady}, [3]string{"🔥 lit | no project selected | claims:0", "🔥 no project | c:0", "🔥 c:0"}},
		"offline":          {litState{Kind: kindOffline, Stage: "impl"}, [3]string{"⚫ not lit", "⚫", "⚫"}},
		"pending":          {litState{Kind: kindPending, Project: "feat/x"}, [3]string{"lit: pending", "lit: pending", "lit: pending"}},
		"no session":       {litState{Kind: kindNoSession}, [3]string{"🔥 lit | no session", "🔥 no session", "🔥"}},
		"failing":          {litState{Kind: kindReady, Project: "feat/x", Claims: 1, Failing: true}, [3]string{"🔥 lit | feat/x | claims:1 | [hooks degraded]", "🔥 feat/x | c:1 | [hooks!]", "🔥 c:1 | [hooks!]"}},
		"failing offline":  {litState{Kind: kindOffline, Failing: true}, [3]string{"⚫ not lit | [hooks degraded]", "⚫ | [hooks!]", "⚫ | [hooks!]"}},
		"failing and idle": {litState{Kind: kindReady, Project: "feat/x", Claims: 1, Stage: "triage", Failing: true}, [3]string{"🔥 lit | feat/x | claims:1 | triage | [hooks degraded]", "🔥 feat/x | c:1 | triage | [hooks!]", "🔥 c:1 | triage | [hooks!]"}},
	} {
		for i, level := range []int{0, 5, maxLevel} {
			if got := strings.Join(c.state.segments(level), " | "); got != c.want[i] {
				t.Errorf("%s at level %d:\n got %q\nwant %q", name, level, got, c.want[i])
			}
		}
	}
}

func TestFitPicksTheFullestLevelThatFits(t *testing.T) {
	d := sample(working)
	for level, line := range collapseOrder {
		cols := displayWidth(line) + 2 // two columns stay spare
		if got := fit(cols, d.render); got != line {
			t.Errorf("level %d fits exactly in %d columns, got %q", level, cols, got)
		}
		if level < maxLevel && collapseOrder[level+1] != line {
			if got := fit(cols-1, d.render); got != collapseOrder[level+1] {
				t.Errorf("one column less than level %d must collapse to level %d, got %q", level, level+1, got)
			}
		}
	}
	if got := fit(0, d.render); got != collapseOrder[0] {
		t.Errorf("an unknown width must not collapse: %q", got)
	}
	if got := fit(5, d.render); got != collapseOrder[maxLevel] {
		t.Errorf("nothing fits, the smallest form is used: %q", got)
	}
}

func TestWithOriginalPutsLitAfterTheLastLineAndShortensItFirst(t *testing.T) {
	const original = "my line | ~/proj" // 16 columns
	full := original + " | 🔥 lit | feat/tracker | claims:3 | impl"
	if got := withOriginal(original, working, 200); got != full {
		t.Errorf("wide:\n got %q\nwant %q", got, full)
	}
	if got := withOriginal(original, working, 0); got != full {
		t.Errorf("unknown width: %q", got)
	}
	// 16 + 3 + 28 = 47 columns with the compact form, 58 with the full one and
	// 32 with the smallest; two columns stay spare.
	got := withOriginal(original, working, 56)
	if want := original + " | 🔥 feat/tracker | c:3 | impl"; got != want {
		t.Errorf("narrow:\n got %q\nwant %q", got, want)
	}
	if got := withOriginal(original, working, 40); got != original+" | 🔥 c:3 | impl" {
		t.Errorf("tiny: %q", got)
	}
	if got := withOriginal(original, working, 10); got != original+" | 🔥 c:3 | impl" {
		t.Errorf("the original is never shortened: %q", got)
	}
	if got := withOriginal("first\nsecond", working, 200); got != "first\nsecond | 🔥 lit | feat/tracker | claims:3 | impl" {
		t.Errorf("only the last line gets lit: %q", got)
	}
	if got := withOriginal("\x1b[32mgreen\x1b[0m", working, 200); got != "\x1b[32mgreen\x1b[0m | 🔥 lit | feat/tracker | claims:3 | impl" {
		t.Errorf("escape sequences take no room: %q", got)
	}
}

func TestOriginalColumnsLeavesLitsSmallestPart(t *testing.T) {
	// "🔥 c:3 | impl" is 13 columns, plus " | " and two spare columns.
	if got := originalColumns(100, working); got != 100-2-3-13 {
		t.Errorf("got %d", got)
	}
	if got := originalColumns(10, working); got != 1 {
		t.Errorf("a window too narrow for lit still gives the original a column: %d", got)
	}
	if got := originalColumns(0, working); got != 0 {
		t.Errorf("an unknown width stays unknown: %d", got)
	}
}
