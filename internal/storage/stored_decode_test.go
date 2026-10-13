package storage

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/nerdwave-nick/lit/internal/protocol"
)

// strictTransaction decodes a stored transaction as the request API does,
// with protocol.Decode: the reference the stored decoding must equal.
func strictTransaction(t *testing.T, intent, results string) (protocol.Intent, []protocol.ChangedObject) {
	t.Helper()
	var i protocol.Intent
	var c []protocol.ChangedObject
	if e := protocol.Decode([]byte(intent), &i); e != nil {
		t.Fatal(e)
	}
	if e := protocol.Decode([]byte(results), &c); e != nil {
		t.Fatal(e)
	}
	return i, c
}

func TestDecodeTransactionEqualsStrictDecoding(t *testing.T) {
	five := int64(5)
	title, body, empty := "T <&> \u2028 é", "line\nnext\r\n\"quoted\"\t", ""
	refs, labels := []string{"r"}, []string{}
	var none *string
	intents := []protocol.Intent{
		{SchemaVersion: 1, Operation: "transaction", ServiceID: testService, Actor: testActor, Force: true,
			Operations: []protocol.Operation{
				{Type: Issues, ID: id(1), Kind: "update", Set: protocol.ProjectSet{Title: &title, Body: &body, Labels: &labels, Assignee: &none, ParentID: &none},
					Add: protocol.ProjectMembers{Blocks: []string{id(2)}}, Remove: protocol.ProjectMembers{Related: []string{id(3)}}},
				{Type: Projects, ID: id(4), Kind: "create", Set: protocol.ProjectSet{Description: &empty, RepositoryRefs: &refs}},
			},
			Targets: []protocol.ObjectRef{{Type: Issues, ID: id(1), ExpectedRevision: &five}, {Type: Projects, ID: id(4)}},
			Guards:  []protocol.ObjectRef{{Type: Issues, ID: id(2), ExpectedRevision: &five}},
		},
	}
	assignee, parent := "me", id(9)
	intents = append(intents, intents[0])
	intents[1].Operations = []protocol.Operation{{Type: Issues, ID: id(1), Kind: "update", Set: protocol.ProjectSet{Assignee: func() **string { p := &assignee; return &p }(), ParentID: func() **string { p := &parent; return &p }()}}}
	results := []protocol.ChangedObject{{Type: Issues, ID: id(1), BeforeRevision: 4, Revision: 5}, {Type: Projects, ID: id(4), Revision: 1}}
	for n, in := range intents {
		stored, _, e := protocol.Canonical(in)
		if e != nil {
			t.Fatal(e)
		}
		res, _ := json.Marshal(results)
		wantI, wantR := strictTransaction(t, string(stored), string(res))
		gotI, gotR, e := decodeTransaction(string(stored), string(res))
		if e != nil {
			t.Fatal(n, e)
		}
		if !reflect.DeepEqual(gotI, wantI) || !reflect.DeepEqual(gotR, wantR) {
			t.Fatalf("intent %d:\ngot  %#v\nwant %#v", n, gotI, wantI)
		}
		g, _ := json.Marshal(gotI)
		w, _ := json.Marshal(wantI)
		if string(g) != string(w) {
			t.Fatalf("intent %d encodes as\n%s\nnot\n%s", n, g, w)
		}
	}
}

func TestDecodeTransactionRejectsCorruptRows(t *testing.T) {
	for _, intent := range []string{`{"schema_version":1,"schema_version":1}`, `{"unknown":1}`, `[`, "{\"operation\":\"\xff\"}"} {
		if _, _, e := decodeTransaction(intent, "[]"); e == nil {
			t.Errorf("%q decoded", intent)
		}
	}
}

// TestDecodeTransactionEqualsStrictDecoding covers every **string field and
// every type with its own UnmarshalJSON of a stored transaction; storedJSON
// rebuilds what those methods add. A new one needs a case there.
func TestStoredTransactionsHaveKnownSpecialCases(t *testing.T) {
	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	var pointers, methods []string
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		if seen[typ] {
			return
		}
		seen[typ] = true
		if typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(unmarshaler) {
			methods = append(methods, typ.Name())
		}
		switch typ.Kind() {
		case reflect.Struct:
			for f := range typ.Fields() {
				if !f.IsExported() || f.Tag.Get("json") == "-" {
					continue
				}
				if f.Type == reflect.TypeFor[**string]() {
					pointers = append(pointers, typ.Name()+"."+f.Name)
				}
				walk(f.Type)
			}
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(typ.Elem())
		}
	}
	walk(reflect.TypeFor[protocol.Intent]())
	walk(reflect.TypeFor[[]protocol.ChangedObject]())
	slices.Sort(pointers)
	slices.Sort(methods)
	if want := []string{"ProjectSet.Assignee", "ProjectSet.ParentID"}; !slices.Equal(pointers, want) {
		t.Errorf("**string fields %v, want %v", pointers, want)
	}
	if want := []string{"Intent", "ProjectMembers", "ProjectSet"}; !slices.Equal(methods, want) {
		t.Errorf("types with UnmarshalJSON %v, want %v", methods, want)
	}
}
