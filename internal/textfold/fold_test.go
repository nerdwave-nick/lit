package textfold

import (
	"math/rand"
	"testing"
	"testing/quick"
)

func TestFoldIsFullCaseFoldingBetweenNFC(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"Straße", "strasse"},
		{"STRASSE", "strasse"},
		{"Ünïcode", "ünïcode"},
		{"Ü", "ü"},                    // a combining diaeresis composes after folding
		{"K", "k"},                     // the Kelvin sign folds to k
		{"ΣΊΣΥΦΟΣ", "σίσυφοσ"},         // full folding has no final-sigma context
		{"line\r\nTwo", "line\r\ntwo"}, // line breaks are kept
	} {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The storage module folds each field separately and joins folded fields with
// a line break; that equals folding the joined text only if a line break is a
// folding boundary, even next to combining marks.
func TestFoldTreatsLineBreaksAsBoundaries(t *testing.T) {
	alphabet := []rune("aAßẞΣςKkĶ́̈éÉ\r\n İıﬃ")
	gen := func(r *rand.Rand) string {
		b := make([]rune, r.Intn(8))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		return string(b)
	}
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		a, b := gen(r), gen(r)
		return Fold(a+"\n"+b) == Fold(a)+"\n"+Fold(b)
	}
	if e := quick.Check(f, &quick.Config{MaxCount: 5000}); e != nil {
		t.Fatal(e)
	}
}
