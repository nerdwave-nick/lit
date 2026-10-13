package bench

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func testPool() *TextPool {
	var files [][]string
	for f := 0; f < 5; f++ {
		var lines []string
		for l := 0; l < 400; l++ {
			lines = append(lines, fmt.Sprintf("file %d line %d: func handler(w http.ResponseWriter) Straße café net/http C++ ✓", f, l))
		}
		files = append(files, lines)
	}
	return NewTextPool(files)
}

// Bodies follow the live tracker: median about 870 B, maximum about 15 KB.
func TestBodySizesAreShapedLikeTheLiveTracker(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var sizes []int
	for i := 0; i < 20000; i++ {
		sizes = append(sizes, bodySize(rng))
	}
	slices.Sort(sizes)
	median := sizes[len(sizes)/2]
	if median < 800 || median > 950 {
		t.Fatalf("median %d", median)
	}
	if sizes[len(sizes)-1] > MaxBody || sizes[0] < 1 {
		t.Fatalf("range %d..%d", sizes[0], sizes[len(sizes)-1])
	}
	if sizes[len(sizes)*99/100] < 5000 {
		t.Fatalf("p99 %d: no long bodies", sizes[len(sizes)*99/100])
	}
}

func TestTextHasTheRequestedSizeAndIsValidUTF8(t *testing.T) {
	pool := testPool()
	rng := rand.New(rand.NewPCG(3, 4))
	for _, size := range []int{1, 50, 870, 15 * 1024, BigBody} {
		s := pool.Text(rng, size)
		if len(s) > size || len(s) < size-4 || !utf8.ValidString(s) {
			t.Fatalf("size %d: got %d bytes, valid %v", size, len(s), utf8.ValidString(s))
		}
	}
}

func TestEditBodyChangesAFewLinesWithinTheLimit(t *testing.T) {
	pool := testPool()
	rng := rand.New(rand.NewPCG(5, 6))
	old := pool.Text(rng, 2000)
	for i := 0; i < 200; i++ {
		next := editBody(rng, pool, old, 2100)
		if next == old || len(next) > 2100 || !utf8.ValidString(next) {
			t.Fatalf("edit %d: %d bytes, same %v", i, len(next), next == old)
		}
		// Most lines survive an edit: it is a small diff, not a rewrite.
		kept := 0
		for _, line := range strings.Split(next, "\n") {
			if strings.Contains(old, line) {
				kept++
			}
		}
		if kept < strings.Count(next, "\n")/2 {
			t.Fatalf("edit %d rewrote the body", i)
		}
		old = next
	}
	if editBody(rng, pool, "", 100) == "" {
		t.Fatal("an empty body stayed empty")
	}
}

func testShape() Shape {
	return Shape{Issues: 15, Comments: 5, Updates: 5, LongLived: true, LongRevisions: 205, Big: true, Links: 3, Milestones: 2}
}

func TestPlanProjectShape(t *testing.T) {
	plan := PlanProject(testPool(), 42, 7, testShape())
	if len(plan.Txns) == 0 || plan.Txns[0].Operation != "project.create" {
		t.Fatal("first transaction does not create the project")
	}
	created := map[string]int{} // issue ID -> transaction index
	counts := map[string]int{}
	updates := map[string]int{}
	for n, txn := range plan.Txns {
		if len(txn.Items) < 1 || len(txn.Items) > 5 {
			t.Fatalf("transaction %d has %d items", n, len(txn.Items))
		}
		seen := map[string]bool{}
		for _, item := range txn.Items {
			counts[txn.Operation]++
			switch txn.Operation {
			case "issue.create":
				if item.Parent != nil {
					if _, ok := created[*item.Parent]; !ok {
						t.Fatalf("issue %s created before its parent", item.ID)
					}
				}
				created[item.ID] = n
			case "issue.update":
				if seen[item.Target] {
					t.Fatalf("transaction %d updates %s twice", n, item.Target)
				}
				seen[item.Target] = true
				updates[item.Target]++
				if _, ok := created[item.Target]; !ok {
					t.Fatal("update before creation")
				}
			case "issue.link":
				if created[item.From] >= n || created[item.To[0]] >= n {
					t.Fatal("link before creation")
				}
			case "comment.create":
				if _, ok := created[item.Issue]; !ok {
					t.Fatal("comment before its issue")
				}
			}
		}
	}
	issues := counts["issue.create"]
	if issues < 13 || issues > 17 || len(plan.Issues) != issues {
		t.Fatalf("%d issues", issues)
	}
	if c := float64(counts["comment.create"]) / float64(issues); c < 3 || c > 7 {
		t.Fatalf("%.1f comments per issue", c)
	}
	long, big := 0, 0
	for _, issue := range plan.Issues {
		if issue.LongLived {
			long++
			if updates[issue.ID] < 200 {
				t.Fatalf("long-lived issue has %d updates", updates[issue.ID])
			}
		} else if updates[issue.ID] < 3 || updates[issue.ID] > 7 {
			t.Fatalf("issue has %d updates", updates[issue.ID])
		}
		if len(issue.Body) > BigBody {
			t.Fatalf("body of %d bytes", len(issue.Body))
		}
		if len(issue.Body) > MaxBody {
			big++
		}
	}
	if long != 1 || big != 1 || counts["issue.link"] != 3 || counts["milestone.create"] != 2 {
		t.Fatalf("long %d big %d links %d milestones %d", long, big, counts["issue.link"], counts["milestone.create"])
	}
}

func TestPlanProjectIsDeterministic(t *testing.T) {
	a, _ := json.Marshal(PlanProject(testPool(), 42, 3, testShape()))
	b, _ := json.Marshal(PlanProject(testPool(), 42, 3, testShape()))
	c, _ := json.Marshal(PlanProject(testPool(), 42, 4, testShape()))
	if string(a) != string(b) || string(a) == string(c) {
		t.Fatal("plans are not deterministic per project")
	}
}

func TestUpdatesTouchDistinctIssues(t *testing.T) {
	plan := PlanProject(testPool(), 1, 1, testShape())
	rng := rand.New(rand.NewPCG(9, 9))
	txns := UpdateTxns(rng, testPool(), plan.Issues, 100)
	rows := 0
	for _, txn := range txns {
		if txn.Operation != "issue.update" || len(txn.Items) < 1 || len(txn.Items) > 5 {
			t.Fatalf("%s with %d items", txn.Operation, len(txn.Items))
		}
		seen := map[string]bool{}
		for _, item := range txn.Items {
			if seen[item.Target] {
				t.Fatal("duplicate target")
			}
			seen[item.Target] = true
		}
		rows += len(txn.Items)
	}
	if rows < 100 || rows > 104 {
		t.Fatalf("%d history rows for 100 requested", rows)
	}
}

// Replaying a plan in order, every update changes its issue (lit answers an
// unchanged update already_satisfied, which writes no history), and the
// final state is the plan's issue state.
func TestEveryPlannedUpdateChangesItsIssue(t *testing.T) {
	for index := range 30 {
		replayPlan(t, PlanProject(testPool(), 42, index, testShape()))
	}
}

func replayPlan(t *testing.T, plan *ProjectPlan) {
	t.Helper()
	type issue struct {
		body, state string
		labels      []string
	}
	state := map[string]*issue{}
	for n, txn := range plan.Txns {
		for _, item := range txn.Items {
			switch txn.Operation {
			case "issue.create":
				state[item.ID] = &issue{*item.Content, "open", item.Labels}
			case "issue.update":
				st := state[item.Target]
				before := *st
				if item.Set.Body != nil {
					st.body = *item.Set.Body
				}
				if item.Set.State != nil {
					st.state = *item.Set.State
				}
				if item.Set.Labels != nil {
					st.labels = *item.Set.Labels
				}
				if st.body == before.body && st.state == before.state && slices.Equal(st.labels, before.labels) {
					t.Fatalf("transaction %d leaves %s unchanged", n, item.Target)
				}
			}
		}
	}
	for _, st := range plan.Issues {
		if state[st.ID].body != st.Body || !slices.Equal(state[st.ID].labels, st.Labels) {
			t.Fatalf("issue %s: replayed state differs from the plan's", st.ID)
		}
	}
}
