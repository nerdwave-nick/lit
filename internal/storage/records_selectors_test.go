package storage

import (
	"context"
	"testing"
)

func TestSelectorsResolveIDPrefixes(t *testing.T) {
	w := newWorld(t)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		for _, c := range []struct {
			typ, prefix, project string
			limit                int
			want                 []string
		}{
			{Issues, "00000000-0000-4000-8000-000000000011", "", 0, []string{w.a.ID}},
			{Issues, "00000000000040008000000000000011", "", 0, []string{w.a.ID}},
			{Issues, "00000000-0000-4000-8000-00000000001", "", 0, []string{w.a.ID, w.b.ID, w.c.ID, w.x.ID}},
			{Issues, "0000000000004000800000000000001", "", 2, []string{w.a.ID, w.b.ID}},
			{Issues, "0000000000004000800000000000001", w.q.ID, 0, []string{w.x.ID}},
			{Issues, "00000000", w.p.ID, 0, []string{w.a.ID, w.b.ID, w.c.ID}},
			{Projects, "0000000000004000800000000000000", "", 0, []string{w.p.ID, w.q.ID}},
			{Comments, "0000000000004000800000000000002", "", 0, []string{w.comment.ID}},
			{Milestones, "0000000000004000800000000000003", w.p.ID, 0, []string{w.milestone.ID}},
			{Milestones, "0000000000004000800000000000003", w.q.ID, 0, []string{}},
			{Issues, "000000000000400080000000000000", "", 0, []string{w.a.ID, w.b.ID, w.c.ID, w.x.ID}},
			{Issues, "ffff", "", 0, []string{}},
		} {
			got, e := r.IDsByPrefix(ctx, c.typ, c.prefix, c.project, c.limit)
			if e != nil {
				t.Fatal(c, e)
			}
			sameJSON(t, got, c.want)
		}
		for _, bad := range []string{"", "xyz", "0000-0000", "00000000-0000-4000-8000-0000000000111"} {
			if _, e := r.IDsByPrefix(ctx, Issues, bad, "", 0); e == nil {
				t.Fatalf("accepted prefix %q", bad)
			}
		}
	})
}

func TestSelectorsResolveTitles(t *testing.T) {
	w := newWorld(t)
	k := newProject(id(3), "feat/k-s", "", at(2))
	mustApply(t, w.db, at(2), k)
	read(t, w.db, func(ctx context.Context, r *ReadTx) {
		for _, c := range []struct {
			typ, project, title, want string
		}{
			{Projects, "", "feat/one", w.p.ID},
			{Projects, "", "FEAT/ONE", w.p.ID},
			{Projects, w.q.ID, "feat/one", w.p.ID}, // project titles are global
			{Projects, "", "feat/K-ſ", k.ID},       // Kelvin sign and long s fold to ASCII
			{Projects, "", "feat/one/", ""},
			{Projects, "", "feat/öne", ""},
			{Issues, w.p.ID, "STRASSE", w.a.ID},
			{Issues, w.q.ID, "straße", w.x.ID},
			{Issues, w.q.ID, "B", ""},
			{Issues, "", "B", ""},
			{Milestones, w.p.ID, "m1", w.milestone.ID},
			{Milestones, w.q.ID, "M1", ""},
		} {
			got, found, e := r.IDByTitle(ctx, c.typ, c.project, c.title)
			if e != nil || found != (c.want != "") || got != c.want {
				t.Fatalf("%v: %q %v %v", c, got, found, e)
			}
		}
		if _, _, e := r.IDByTitle(ctx, Comments, w.p.ID, "x"); e == nil {
			t.Fatal("comments have no title")
		}
	})
}
