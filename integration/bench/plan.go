package bench

import (
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

// Shape is the shape of one project of the corpus (decision 341b612a).
type Shape struct {
	// Issues is the mean number of issues; each project has Issues±2.
	Issues int
	// Comments and Updates are means per issue; each issue gets the mean±2.
	Comments, Updates int
	// LongLived gives the first issue LongRevisions updates instead.
	LongLived     bool
	LongRevisions int
	// Big gives the second issue a BigBody body.
	Big bool
	// Links is the number of blocks relationships, Milestones the number of
	// milestones of 3-6 issues each.
	Links, Milestones int
}

// ProjectShape is the corpus shape of project index: about 15 issues with
// about 5 comments and 5 updates each, 3 blockers, 1-2 milestones. "A few"
// issues get 205 updates and "a few" bodies are 64 KB: one of each in every
// 20th project, in different projects (5 each at the target size).
func ProjectShape(index int) Shape {
	return Shape{Issues: 15, Comments: 5, Updates: 5, LongLived: index%20 == 10, LongRevisions: 205, Big: index%20 == 0, Links: 3, Milestones: 1 + index%2}
}

// Txn is one transaction as lit prepares it: one operation with 1-5 items.
type Txn struct {
	Operation string
	Project   string
	Items     []protocol.ProjectInput
}

// IssueState is an issue's current state as the corpus generated it.
type IssueState struct {
	ID, Body          string
	Labels            []string
	Closed, LongLived bool
}

// ProjectPlan is a project and the transactions that create its history,
// in order. Issues hold the state after every transaction committed.
type ProjectPlan struct {
	ID, Title string
	Txns      []Txn
	Issues    []*IssueState
}

var labelSet = []string{"bug", "enhancement", "needs-info", "needs-triage", "ready-for-agent", "ready-for-human"}

// PlanProject generates project index deterministically from seed.
func PlanProject(pool *TextPool, seed uint64, index int, shape Shape) *ProjectPlan {
	rng := rand.New(rand.NewPCG(seed, uint64(index)))
	p := &ProjectPlan{ID: uuid(rng), Title: fmt.Sprintf("feat/bench-%04d", index)}
	description := pool.Text(rng, bodySize(rng))
	p.Txns = append(p.Txns, Txn{Operation: "project.create", Items: []protocol.ProjectInput{{ID: p.ID, Title: &p.Title, Content: &description}}})

	var creates []protocol.ProjectInput
	for i := range shape.Issues - 2 + rng.IntN(5) {
		size := bodySize(rng)
		if shape.Big && i == 1 {
			size = BigBody
		}
		st := &IssueState{ID: uuid(rng), Body: pool.Text(rng, size), Labels: randomLabels(rng), LongLived: shape.LongLived && i == 0}
		title := fmt.Sprintf("%s [%d.%d]", pool.Title(rng), index, i)
		body := st.Body // st.Body moves on with the planned updates
		item := protocol.ProjectInput{ID: st.ID, Title: &title, Content: &body, Labels: st.Labels}
		if i >= 3 && rng.Float64() < 0.4 {
			parent := p.Issues[rng.IntN(i)].ID
			item.Parent = &parent
		}
		p.Issues = append(p.Issues, st)
		creates = append(creates, item)
	}
	p.Txns = append(p.Txns, packItems(rng, "issue.create", p.ID, creates)...)

	var links []protocol.ProjectInput
	seen := map[[2]int]bool{}
	for len(links) < shape.Links {
		a, b := rng.IntN(len(p.Issues)), rng.IntN(len(p.Issues))
		if a < b && !seen[[2]int{a, b}] { // a blocks b only for a < b: no cycles
			seen[[2]int{a, b}] = true
			links = append(links, protocol.ProjectInput{From: p.Issues[a].ID, To: []string{p.Issues[b].ID}, Relation: "blocks"})
		}
	}
	p.Txns = append(p.Txns, packItems(rng, "issue.link", p.ID, links)...)

	for m := range shape.Milestones {
		title := fmt.Sprintf("Milestone %d of %s", m+1, p.Title)
		body := pool.Text(rng, bodySize(rng))
		var members []string
		for _, n := range rng.Perm(len(p.Issues))[:3+rng.IntN(4)] {
			members = append(members, p.Issues[n].ID)
		}
		slices.Sort(members)
		item := protocol.ProjectInput{ID: uuid(rng), Project: p.ID, Title: &title, Content: &body, IssueIDs: members}
		p.Txns = append(p.Txns, Txn{Operation: "milestone.create", Project: p.ID, Items: []protocol.ProjectInput{item}})
	}

	// Comments and updates, interleaved in random order.
	type work struct {
		comment bool
		issue   *IssueState
	}
	var queue []work
	for _, st := range p.Issues {
		for range shape.Comments - 2 + rng.IntN(5) {
			queue = append(queue, work{true, st})
		}
		updates := shape.Updates - 2 + rng.IntN(5)
		if st.LongLived {
			updates = shape.LongRevisions
		}
		for range updates {
			queue = append(queue, work{false, st})
		}
	}
	rng.Shuffle(len(queue), func(i, j int) { queue[i], queue[j] = queue[j], queue[i] })
	b := batcher{rng: rng, project: p.ID}
	for _, w := range queue {
		if w.comment {
			body := pool.Text(rng, bodySize(rng))
			b.add("comment.create", "", protocol.ProjectInput{ID: uuid(rng), Issue: w.issue.ID, Content: &body})
		} else {
			b.add("issue.update", w.issue.ID, updateItem(rng, pool, w.issue))
		}
	}
	p.Txns = append(p.Txns, b.done()...)
	return p
}

// UpdateTxns generates update-only transactions of random issues that add
// at least rows history rows (one per updated issue).
func UpdateTxns(rng *rand.Rand, pool *TextPool, issues []*IssueState, rows int) []Txn {
	b := batcher{rng: rng}
	for n := 0; n < rows; n++ {
		st := issues[rng.IntN(len(issues))]
		b.add("issue.update", st.ID, updateItem(rng, pool, st))
	}
	return b.done()
}

// updateItem changes st as an agent would: mostly a small body edit,
// sometimes its labels or its state. Every update changes something.
func updateItem(rng *rand.Rand, pool *TextPool, st *IssueState) protocol.ProjectInput {
	item := protocol.ProjectInput{Target: st.ID}
	switch r := rng.Float64(); {
	case r < 0.7:
		limit := MaxBody
		if len(st.Body) > MaxBody {
			limit = BigBody
		}
		st.Body = editBody(rng, pool, st.Body, limit)
		body := st.Body
		item.Set.Body = &body
	case r < 0.9:
		labels := randomLabels(rng)
		for slices.Equal(labels, st.Labels) {
			labels = randomLabels(rng)
		}
		st.Labels = labels
		item.Set.Labels = &labels
	default:
		st.Closed = !st.Closed
		state := map[bool]string{false: "open", true: "closed"}[st.Closed]
		item.Set.State = &state
	}
	return item
}

// batcher packs items into transactions of 1-5 items of one operation,
// never naming the same issue twice in one transaction.
type batcher struct {
	rng     *rand.Rand
	project string
	txns    []Txn
	cur     Txn
	size    int
	issues  map[string]bool
}

func (b *batcher) add(op, issue string, item protocol.ProjectInput) {
	if b.cur.Operation != op || len(b.cur.Items) == b.size || issue != "" && b.issues[issue] {
		b.flush()
		b.cur = Txn{Operation: op, Project: b.project}
		b.size = 1 + b.rng.IntN(5)
	}
	b.cur.Items = append(b.cur.Items, item)
	if issue != "" {
		b.issues[issue] = true
	}
}

func (b *batcher) flush() {
	if len(b.cur.Items) > 0 {
		b.txns = append(b.txns, b.cur)
	}
	b.cur, b.issues = Txn{}, map[string]bool{}
}

func (b *batcher) done() []Txn {
	b.flush()
	return b.txns
}

func packItems(rng *rand.Rand, op, project string, items []protocol.ProjectInput) []Txn {
	b := batcher{rng: rng, project: project}
	for _, item := range items {
		b.add(op, "", item)
	}
	return b.done()
}

func randomLabels(rng *rand.Rand) []string {
	out := []string{}
	for _, n := range rng.Perm(len(labelSet))[:rng.IntN(3)] {
		out = append(out, labelSet[n])
	}
	slices.Sort(out)
	return out
}

// uuid is a random version 4 UUID drawn from rng, so plans are reproducible.
func uuid(rng *rand.Rand) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
