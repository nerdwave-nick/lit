package export

import (
	"context"
	"sort"

	"github.com/nerdwave-nick/lit/internal/protocol"
	"github.com/nerdwave-nick/lit/internal/storage"
)

// tracker is every exported record, read from one snapshot, with the paths
// of their files. Records are ordered by ID; comments by creation.
type tracker struct {
	projects     []protocol.Project
	issues       []protocol.Issue
	milestones   []protocol.Milestone
	comments     map[string][]protocol.Comment // by issue ID
	commentCount int

	project     map[string]*protocol.Project
	issue       map[string]*protocol.Issue
	milestoneOf map[string][]string // issue ID -> milestone IDs

	dir           map[string]string // project ID -> directory
	issueFile     map[string]string // issue ID -> file
	milestoneFile map[string]string // milestone ID -> file
}

func list[T any](ctx context.Context, snap Snapshot, q storage.ListQuery) ([]T, error) {
	rs, e := snap.List(ctx, q)
	out := make([]T, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.(T))
	}
	return out, e
}

func load(ctx context.Context, snap Snapshot) (*tracker, error) {
	t := &tracker{comments: map[string][]protocol.Comment{}, project: map[string]*protocol.Project{}, issue: map[string]*protocol.Issue{}, milestoneOf: map[string][]string{}}
	var e error
	if t.projects, e = list[protocol.Project](ctx, snap, storage.ListQuery{Type: storage.Projects}); e != nil {
		return nil, e
	}
	if t.issues, e = list[protocol.Issue](ctx, snap, storage.ListQuery{Type: storage.Issues}); e != nil {
		return nil, e
	}
	for _, p := range t.projects {
		ms, e := list[protocol.Milestone](ctx, snap, storage.ListQuery{Type: storage.Milestones, ProjectID: p.ID})
		if e != nil {
			return nil, e
		}
		t.milestones = append(t.milestones, ms...)
	}
	for _, i := range t.issues {
		cs, e := list[protocol.Comment](ctx, snap, storage.ListQuery{Type: storage.Comments, IssueID: i.ID})
		if e != nil {
			return nil, e
		}
		if len(cs) > 0 {
			t.comments[i.ID] = cs
			t.commentCount += len(cs)
		}
	}
	sort.Slice(t.projects, func(a, b int) bool { return t.projects[a].ID < t.projects[b].ID })
	sort.Slice(t.issues, func(a, b int) bool { return t.issues[a].ID < t.issues[b].ID })
	sort.Slice(t.milestones, func(a, b int) bool { return t.milestones[a].ID < t.milestones[b].ID })
	for k := range t.projects {
		t.project[t.projects[k].ID] = &t.projects[k]
	}
	for k := range t.issues {
		t.issue[t.issues[k].ID] = &t.issues[k]
	}
	for _, m := range t.milestones {
		for _, id := range m.IssueIDs {
			t.milestoneOf[id] = append(t.milestoneOf[id], m.ID)
		}
	}
	t.layout()
	return t, nil
}

// layout assigns every record its file. Names follow the titles; when two
// records would share a name, the older record keeps it and the newer one
// gets a longer ID (issues) or an ID suffix (milestones and project
// directories), so existing names stay put.
func (t *tracker) layout() {
	t.dir, t.issueFile, t.milestoneFile = map[string]string{}, map[string]string{}, map[string]string{}
	taken := map[string]bool{}
	claim := func(name, fallback string) string {
		if taken[name] {
			name = fallback
		}
		taken[name] = true
		return name
	}
	byAge := func(n int, created func(int) string, id func(int) string) []int {
		order := make([]int, n)
		for k := range order {
			order[k] = k
		}
		sort.Slice(order, func(a, b int) bool {
			ca, cb := created(order[a]), created(order[b])
			return ca < cb || ca == cb && id(order[a]) < id(order[b])
		})
		return order
	}
	for _, k := range byAge(len(t.projects), func(k int) string { return t.projects[k].CreatedAt }, func(k int) string { return t.projects[k].ID }) {
		p := t.projects[k]
		d := projectDir(p.Title)
		t.dir[p.ID] = claim(d, d+"-"+p.ID[:8])
	}
	for _, k := range byAge(len(t.issues), func(k int) string { return t.issues[k].CreatedAt }, func(k int) string { return t.issues[k].ID }) {
		i := t.issues[k]
		base := t.dir[i.ProjectID] + "/issues/"
		t.issueFile[i.ID] = claim(base+i.ID[:8]+"-"+slug(i.Title)+".md", base+i.ID+"-"+slug(i.Title)+".md")
	}
	for _, k := range byAge(len(t.milestones), func(k int) string { return t.milestones[k].CreatedAt }, func(k int) string { return t.milestones[k].ID }) {
		m := t.milestones[k]
		base := t.dir[m.ProjectID] + "/milestones/" + slug(m.Title)
		t.milestoneFile[m.ID] = claim(base+".md", base+"-"+m.ID[:8]+".md")
	}
}
