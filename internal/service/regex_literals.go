package service

import (
	"regexp/syntax"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/textfold"
)

// The regex prefilter. lit grep -E reads only the records whose folded text
// (textfold.Grep, as grep_text stores it) contains one of the literals that
// every match of the pattern requires. Literals are folded scalar by scalar,
// so a match's fold contains them whatever the pattern's case flags: a
// case-sensitive rune folds as itself, and a case-insensitive one as any
// member of its RE2 (simple) case-fold orbit, which lit's full fold does not
// always map to one string (Cherokee, the newest Latin letters).

const (
	// maxExact bounds the strings of one exact set: alternatives and
	// case-insensitive orbits multiply them.
	maxExact = 16
	// maxClass is the largest character class expanded into its runes.
	maxClass = 8
	// maxNeedles is the most literals the prefilter looks up for one pattern.
	maxNeedles = 32
	// minNeedle is the shortest literal the trigram index can look up.
	minNeedle = 3
)

// literals is what a regex node tells about the fold of its matches.
type literals struct {
	// exact, when not nil, holds every string the fold of a match can be.
	exact []string
	// all holds sets of strings: the fold of a match contains one string of
	// each set.
	all [][]string
}

// regexNeedles returns folded literals one of which the fold of every match
// of re contains: the best set of literals of at least minNeedle characters,
// at most maxNeedles of them. It returns nil when there is none, and then
// every record is a candidate.
func regexNeedles(re *syntax.Regexp) []string {
	return best(analyze(re).sets())
}

func analyze(re *syntax.Regexp) literals {
	fold := re.Flags&syntax.FoldCase != 0
	switch re.Op {
	case syntax.OpLiteral:
		parts := make([]literals, len(re.Rune))
		for i, r := range re.Rune {
			parts[i] = literals{exact: foldSet(r, fold)}
		}
		return concat(parts)
	case syntax.OpCharClass:
		return charClass(re.Rune)
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return literals{exact: []string{""}}
	case syntax.OpCapture:
		return analyze(re.Sub[0])
	case syntax.OpConcat:
		parts := make([]literals, len(re.Sub))
		for i, sub := range re.Sub {
			parts[i] = analyze(sub)
		}
		return concat(parts)
	case syntax.OpAlternate:
		return alternate(re.Sub)
	case syntax.OpQuest:
		if l := analyze(re.Sub[0]); l.exact != nil && len(l.exact) < maxExact {
			return literals{exact: foldUnion(l.exact, []string{""})}
		}
	case syntax.OpPlus:
		return atLeastOnce(analyze(re.Sub[0]))
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return atLeastOnce(analyze(re.Sub[0]))
		}
	}
	// Any character, stars, optional repeats, and no match at all.
	return literals{}
}

// foldSet is the folds a match of rune r can have: its own, or with fold
// those of every member of its case-fold orbit.
func foldSet(r rune, fold bool) []string {
	out := []string{textfold.Scalar(r)}
	if fold {
		for o := unicode.SimpleFold(r); o != r; o = unicode.SimpleFold(o) {
			out = foldUnion(out, []string{textfold.Scalar(o)})
		}
	}
	return out
}

// charClass expands a class of at most maxClass runes (ranges as pairs).
// Under case folding, RE2's parser has already added each rune's orbit.
func charClass(ranges []rune) literals {
	n := 0
	for i := 0; i < len(ranges); i += 2 {
		n += int(ranges[i+1]-ranges[i]) + 1
	}
	if n == 0 || n > maxClass {
		return literals{}
	}
	var out []string
	for i := 0; i < len(ranges); i += 2 {
		for r := ranges[i]; r <= ranges[i+1]; r++ {
			out = foldUnion(out, foldSet(r, false))
		}
	}
	if len(out) > maxExact {
		return literals{}
	}
	return literals{exact: out}
}

// concat joins adjacent exact parts into longer strings while their product
// stays within maxExact; every other run and part becomes a requirement.
func concat(parts []literals) literals {
	var out literals
	run, whole := []string{""}, true
	for _, p := range parts {
		switch {
		case p.exact == nil:
			out.all = append(append(out.all, run), p.all...)
			run, whole = []string{""}, false
		case len(run)*len(p.exact) > maxExact:
			out.all = append(out.all, run)
			run, whole = p.exact, false
		default:
			run = foldCross(run, p.exact)
		}
	}
	if whole {
		return literals{exact: run}
	}
	out.all = append(out.all, run)
	return out
}

// alternate is exact while its alternatives are and stay within maxExact;
// otherwise each alternative contributes its best set to one requirement,
// which exists only when every alternative has one.
func alternate(subs []*syntax.Regexp) literals {
	parts := make([]literals, len(subs))
	exact := []string{}
	for i, sub := range subs {
		parts[i] = analyze(sub)
		if exact != nil && parts[i].exact != nil && len(exact)+len(parts[i].exact) <= maxExact {
			exact = foldUnion(exact, parts[i].exact)
		} else {
			exact = nil
		}
	}
	if exact != nil {
		return literals{exact: exact}
	}
	var set []string
	for _, p := range parts {
		b := best(p.sets())
		if b == nil {
			return literals{}
		}
		set = foldUnion(set, b)
	}
	return literals{all: [][]string{set}}
}

// atLeastOnce is a node that matches its sub one or more times: it requires
// what one match of sub requires.
func atLeastOnce(sub literals) literals {
	return literals{all: sub.sets()}
}

// sets is every set l requires, its exact set included (nil when unknown).
func (l literals) sets() [][]string { return append(slices.Clip(l.all), l.exact) }

// best is the usable set (all strings at least minNeedle characters, at most
// maxNeedles strings) with the longest shortest string, then the fewest
// strings; nil sets are skipped, and nil means none is usable.
func best(sets [][]string) []string {
	var out []string
	shortest := 0
	for _, set := range sets {
		if len(set) == 0 || len(set) > maxNeedles {
			continue
		}
		n := utf8.RuneCountInString(set[0])
		for _, s := range set {
			n = min(n, utf8.RuneCountInString(s))
		}
		if n >= minNeedle && (n > shortest || n == shortest && len(set) < len(out)) {
			out, shortest = set, n
		}
	}
	return out
}

func foldCross(a, b []string) []string {
	var out []string
	for _, x := range a {
		for _, y := range b {
			out = foldUnion(out, []string{x + y})
		}
	}
	return out
}

// foldUnion appends the strings of b missing from a.
func foldUnion(a, b []string) []string {
	for _, s := range b {
		if !slices.Contains(a, s) {
			a = append(a, s)
		}
	}
	return a
}
