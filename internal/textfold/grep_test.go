package textfold

import (
	"math/rand"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

// Scalar is exactly the per-scalar fold lit grep has always used, for every
// scalar value.
func TestScalarIsTheFullCaseFoldOfOneScalar(t *testing.T) {
	fold := cases.Fold()
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if got, want := Scalar(r), fold.String(string(r)); got != want {
			t.Fatalf("Scalar(%U) = %q, want %q", r, got, want)
		}
	}
}

func TestGrepFoldsEachScalarWithoutNormalizing(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"Straße", "strasse"},
		{"Café", "café"}, // decomposed text stays decomposed
		{"Café", "café"},
		{"K", "k"},             // the Kelvin sign folds to k
		{"İ", "i̇"},            // İ folds to two scalars
		{"ΣΊΣΥΦΟΣ", "σίσυφοσ"}, // no final-sigma context
		{"nul\x00Byte", "nul\x00byte"},
		{"line\r\nTwo", "line\r\ntwo"},
		{"bad\xffbyte", "bad\uFFFDbyte"}, // invalid bytes read as U+FFFD
	} {
		if got := Grep(c.in); got != c.want {
			t.Errorf("Grep(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Grep is a homomorphism: the fold of a concatenation is the concatenation of
// the folds, so a substring of a text folds to a substring of its fold. Fold
// (with NFC) is not: "e" is in "é", but Fold("é") is "é".
func TestGrepKeepsSubstrings(t *testing.T) {
	alphabet := []rune("aAeEßẞΣςσKkḰ̈éÉ\r\n\x00 İıﬃ*?[]%_")
	gen := func(r *rand.Rand) string {
		b := make([]rune, r.Intn(10))
		for i := range b {
			b[i] = alphabet[r.Intn(len(alphabet))]
		}
		return string(b)
	}
	f := func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		a, b, c := gen(r), gen(r), gen(r)
		return Grep(a+b+c) == Grep(a)+Grep(b)+Grep(c) && strings.Contains(Grep(a+b+c), Grep(b))
	}
	if e := quick.Check(f, &quick.Config{MaxCount: 5000}); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(Fold("é"), Fold("e")) {
		t.Fatal("NFC folding unexpectedly keeps the substring")
	}
}
