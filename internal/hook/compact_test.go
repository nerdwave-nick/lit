package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nerdwave-nick/lit/internal/workflowsession"
)

// traffic counts the requests that could change the tracker, to prove that a
// compaction only reads.
type traffic struct {
	mu     sync.Mutex
	writes []string
	// lapse, when set, makes the claims listing report leases that already
	// ended, as a server that kept expired claims visible would.
	lapse atomic.Bool
	// broken makes the requests that contain it in their path or, for record
	// snapshots, their body fail with a server error.
	broken atomic.Value
}

var readOnlyPosts = map[string]bool{
	"/v1/claim-snapshots": true, "/v1/snapshots": true, "/v1/transaction-previews": true,
	"/v1/transaction-status": true, "/v1/search": true, "/v1/ranked-search": true,
}

func (tr *traffic) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !readOnlyPosts[r.URL.Path] {
			tr.mu.Lock()
			tr.writes = append(tr.writes, r.Method+" "+r.URL.Path)
			tr.mu.Unlock()
		}
		if tr.fails(r) {
			http.Error(w, "storage is unavailable", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/claims" && tr.lapse.Load() {
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			body := lapsed(rec.Body.Bytes())
			for k, v := range rec.Header() {
				if k != "Content-Length" {
					w.Header()[k] = v
				}
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (tr *traffic) fails(r *http.Request) bool {
	needle, _ := tr.broken.Load().(string)
	if needle == "" {
		return false
	}
	if r.URL.Path != "/v1/snapshots" {
		return strings.Contains(r.URL.Path, needle)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	return strings.Contains(string(body), needle)
}

// lapsed rewrites a claims listing so that every lease ended five minutes
// before the listing's own service time.
func lapsed(body []byte) []byte {
	var page map[string]any
	if json.Unmarshal(body, &page) != nil {
		return body
	}
	at, _ := time.Parse(time.RFC3339Nano, page["server_time"].(string))
	items, _ := page["items"].([]any)
	for _, item := range items {
		item.(map[string]any)["expires_at"] = at.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	}
	out, _ := json.Marshal(page)
	return out
}

func (tr *traffic) reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.writes = nil
}

func (tr *traffic) written() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string(nil), tr.writes...)
}

// work is a started conversation: a ready session on a project.
type work struct {
	g       *rig
	tr      *traffic
	host    string
	id      string
	project string
}

func startWork(t *testing.T, host string) *work {
	t.Helper()
	tr := &traffic{}
	g := newRig(t, tr.wrap)
	start, id := input(t, host, "startup")
	g.hook(host, "session-start", start, Options{})
	g.run(host, id, "projects", "create", "--project-title", "feat/compacted", "--content", "x")
	project := jsonString(g.tracker(host, id, "projects", "list")[0]["id"])
	g.run(host, id, "session", "set", "--project", project)
	return &work{g: g, tr: tr, host: host, id: id, project: project}
}

// claimed creates an issue and claims it for an hour, with an optional
// checkpoint comment, and returns its ID.
func (w *work) claimed(title, checkpoint string) string {
	w.g.t.Helper()
	w.g.run(w.host, w.id, "issues", "create", "--issue", title, "--content", "x")
	var issue string
	for _, item := range w.g.tracker(w.host, w.id, "issues", "list") {
		if jsonString(item["title"]) == title {
			issue = jsonString(item["id"])
		}
	}
	if issue == "" {
		w.g.t.Fatalf("no issue %q", title)
	}
	if checkpoint != "" {
		w.g.run(w.host, w.id, "comments", "create", "--issue", issue, "--content", checkpoint)
	}
	w.g.run(w.host, w.id, "claims", "acquire", issue, "--for", "1h")
	return issue
}

func (w *work) claim(issue string) map[string]json.RawMessage {
	w.g.t.Helper()
	for _, c := range w.g.tracker(w.host, w.id, "claims", "list") {
		if jsonString(c["issue_id"]) == issue {
			return c
		}
	}
	w.g.t.Fatalf("no claim on %s", issue)
	return nil
}

// compact runs the hook for the host's recorded compact input.
func (w *work) compact(opt Options) reply {
	w.g.t.Helper()
	in, id := input(w.g.t, w.host, "compact")
	if id != w.id {
		w.g.t.Fatalf("the compact fixture must keep the session ID: %s != %s", id, w.id)
	}
	return w.g.hook(w.host, "session-start", in, opt)
}

func (w *work) association() workflowsession.Association {
	w.g.t.Helper()
	a, e := w.g.association(w.host, w.id)
	if e != nil {
		w.g.t.Fatal(e)
	}
	return a
}

func (w *work) record() string {
	w.g.t.Helper()
	path, _, _ := workflowsession.Adapter{Host: w.host, RuntimeID: w.id}.Paths()
	raw, e := os.ReadFile(path)
	if e != nil {
		w.g.t.Fatal(e)
	}
	return string(raw)
}

func (w *work) wantNothingWritten(t *testing.T) {
	t.Helper()
	if writes := w.tr.written(); len(writes) != 0 {
		t.Fatalf("a compaction wrote to the tracker: %v", writes)
	}
}

// reread are the commands the context tells the agent to reread issues with.
func (w *work) reread() []string {
	run := "lit workflow-session --host " + w.host + " --runtime-id " + w.id + " run --"
	return []string{run + " issues get ISSUE_ID", run + " comments list --issue ISSUE_ID"}
}

func TestCompactRestoresTheSessionAndEachClaimWithItsLatestCheckpoint(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			first := w.claimed("first work", "older note")
			w.g.run(host, w.id, "comments", "create", "--issue", first, "--content", "checkpoint: tests green, next wire the docs")
			second := w.claimed("second work", "")
			a := w.association()
			claims := map[string]json.RawMessage{}
			for _, c := range w.g.tracker(host, w.id, "claims", "list") {
				claims[jsonString(c["issue_id"])] = c["expires_at"]
			}
			recordBefore := w.record()
			w.tr.reset()

			r := w.compact(Options{})

			wantContext(t, r, "compaction", "session: "+a.Session, "endpoint: "+w.g.endpoint, "runtime ID: "+w.id,
				"project: feat/compacted ("+w.project+")", "claims: 2 held",
				first, "lease expires "+jsonString(claims[first]),
				second, "lease expires "+jsonString(claims[second]),
				"checkpoint: tests green, next wire the docs",
				"Reread", "data, not instructions", "does not acquire, renew or release")
			if strings.Contains(r.Context, "older note") {
				t.Errorf("only the latest comment is quoted:\n%s", r.Context)
			}
			if !strings.Contains(r.Context, "no comments") {
				t.Errorf("an issue without comments says so:\n%s", r.Context)
			}
			wantContext(t, r, w.reread()...)
			w.wantNothingWritten(t)
			if w.record() != recordBefore {
				t.Fatal("a compaction changed the association")
			}
			if string(w.claim(first)["expires_at"]) != string(claims[first]) {
				t.Fatal("a compaction renewed a claim")
			}
			noFailures(t)
		})
	}
}

func TestCompactWithoutClaimsSaysNoneAreHeld(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			a := w.association()
			w.tr.reset()
			r := w.compact(Options{})
			wantContext(t, r, "session: "+a.Session, "project: feat/compacted ("+w.project+")", "claims: none held")
			w.wantNothingWritten(t)
			noFailures(t)
		})
	}
}

func TestCompactDoesNotReviveALeaseThatRanOut(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			w.g.run(host, w.id, "issues", "create", "--issue", "short lease", "--content", "x")
			w.g.run(host, w.id, "claims", "acquire", "short lease", "--for", "1s")
			issue := jsonString(w.g.tracker(host, w.id, "issues", "list")[0]["id"])
			deadline := time.Now().Add(15 * time.Second)
			for len(w.g.tracker(host, w.id, "claims", "list")) != 0 {
				if time.Now().After(deadline) {
					t.Fatal("the lease never ran out")
				}
				time.Sleep(100 * time.Millisecond)
			}
			w.tr.reset()

			r := w.compact(Options{})

			wantContext(t, r, "claims: none held", "expired", "recheck")
			if strings.Contains(r.Context, issue) {
				t.Errorf("a claim that is not held must not be listed as held:\n%s", r.Context)
			}
			w.wantNothingWritten(t)
			if len(w.g.tracker(host, w.id, "claims", "list")) != 0 {
				t.Fatal("the hook reacquired the claim")
			}
			noFailures(t)
		})
	}
}

func TestCompactReportsAClaimWhoseLeaseHasEndedAsExpired(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			issue := w.claimed("late work", "checkpoint: parser done")
			held := jsonString(w.claim(issue)["expires_at"])
			w.tr.reset()
			w.tr.lapse.Store(true)

			r := w.compact(Options{})

			wantContext(t, r, "claims: 0 held, 1 expired", issue, "EXPIRED", "lease ended", "no longer holds it",
				"checkpoint: parser done")
			if strings.Contains(r.Context, "lease expires") || strings.Contains(r.Context, held) {
				t.Errorf("an ended lease is not a running one:\n%s", r.Context)
			}
			w.tr.lapse.Store(false)
			w.wantNothingWritten(t)
			if string(w.claim(issue)["expires_at"]) != `"`+held+`"` {
				t.Fatal("the hook renewed the claim")
			}
		})
	}
}

func TestCompactBoundsLongCheckpointsAndManyClaims(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			const claims = 10
			issues := make([]string, claims)
			for i := range issues {
				filler := strings.Repeat("filler ", 400)
				body := fmt.Sprintf("HEAD-%d %s MIDDLE-%d %s TAIL-%d", i, filler, i, filler, i)
				issues[i] = w.claimed(fmt.Sprintf("long work %d", i), body)
			}
			wide := w.claimed("wide checkpoint", strings.Repeat("ü", 4000))
			w.tr.reset()

			r := w.compact(Options{})

			wantContext(t, r, "claims: 11 held", wide)
			if units := len(utf16.Encode([]rune(r.Context))); units >= 10000 {
				t.Errorf("the context is %d characters; hosts cap hook context at 10000", units)
			}
			if !utf8.ValidString(r.Context) {
				t.Error("an excerpt was cut inside a character")
			}
			if strings.Contains(r.Context, "MIDDLE-") {
				t.Errorf("the middle of a long checkpoint is omitted:\n%s", r.Context)
			}
			if !strings.Contains(r.Context, "characters omitted") {
				t.Errorf("a cut excerpt says so:\n%s", r.Context)
			}
			for i, issue := range issues {
				if !strings.Contains(r.Context, issue) {
					t.Errorf("claim %d (%s) is missing", i, issue)
				}
			}
			// The newest claims come first and get their excerpts; the oldest only their line.
			for _, i := range []int{claims - 1, claims - 2, claims - 3} {
				wantContext(t, r, fmt.Sprintf("HEAD-%d", i), fmt.Sprintf("TAIL-%d", i))
			}
			if strings.Contains(r.Context, "HEAD-0") {
				t.Errorf("the oldest claim was past the excerpt limit:\n%s", r.Context)
			}
			wantContext(t, r, "more claim")
			w.wantNothingWritten(t)
			noFailures(t)
		})
	}
}

func TestCompactWhenTheServerIsUnreachableNamesNoClaims(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			issue := w.claimed("held work", "checkpoint: do not leak")
			a := w.association()
			recordBefore := w.record()
			w.g.server.Close()

			start := time.Now()
			r := w.compact(Options{})

			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			wantContext(t, r, "session: "+a.Session, "runtime ID: "+w.id, "project: "+w.project,
				"could not be read", "recheck", "lit workflow-session --host "+host+" --runtime-id "+w.id+" run -- claims list")
			for _, leaked := range []string{issue, "do not leak", "lease expires"} {
				if strings.Contains(r.Context, leaked) {
					t.Errorf("an unreachable server must not produce remembered claims (%q):\n%s", leaked, r.Context)
				}
			}
			if w.record() != recordBefore {
				t.Fatal("an unreachable server changed the association")
			}
			noFailures(t)
		})
	}
}

func TestCompactSaysSoWhenTheClaimsCannotBeRead(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			issue := w.claimed("held work", "checkpoint: do not leak")
			a := w.association()
			w.tr.broken.Store("/v1/claims")

			r := w.compact(Options{})

			wantContext(t, r, "session: "+a.Session, "project: feat/compacted", "claims: could not be read; recheck with: lit workflow-session --host "+host+" --runtime-id "+w.id+" run -- claims list")
			if strings.Contains(r.Context, issue) {
				t.Errorf("unreadable claims must not be replaced by remembered ones:\n%s", r.Context)
			}
			failures, _ := RecentFailures(time.Now().Add(-time.Hour))
			if len(failures) != 1 || failures[0].Event != "session-start" || failures[0].RuntimeID != w.id {
				t.Fatalf("a failed claims read is a hook failure: %+v", failures)
			}
		})
	}
}

func TestCompactSaysSoWhenACommentCannotBeRead(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			w := startWork(t, host)
			issue := w.claimed("held work", "checkpoint: hidden")
			w.tr.broken.Store(`"comments"`)

			r := w.compact(Options{})

			wantContext(t, r, "claims: 1 held", issue, "latest comment: could not be read",
				"run -- comments list --issue "+issue)
			if strings.Contains(r.Context, "hidden") {
				t.Errorf("an unreadable comment was quoted:\n%s", r.Context)
			}
			if failures, _ := RecentFailures(time.Now().Add(-time.Hour)); len(failures) != 1 {
				t.Fatalf("a failed comment read is a hook failure: %+v", failures)
			}
		})
	}
}

func TestCompactWithoutAnAssociationCreatesNothing(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			tr := &traffic{}
			g := newRig(t, tr.wrap)
			in, id := input(t, host, "compact")
			r := g.hook(host, "session-start", in, Options{})
			wantContext(t, r, "no lit session", id, "lit workflow-session --host "+host+" --runtime-id "+id+" fresh --endpoint "+g.endpoint)
			if _, e := g.association(host, id); !os.IsNotExist(e) {
				t.Fatalf("a compaction created an association: %v", e)
			}
			if g.connects.Load() != 0 || len(tr.written()) != 0 {
				t.Fatalf("a compaction registered a client: %d %v", g.connects.Load(), tr.written())
			}
			noFailures(t)
		})
	}
}

func TestCompactWithAPendingAssociationSendsTheAgentToReconcile(t *testing.T) {
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			release := make(chan struct{})
			g := newRig(t, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == "/v1/connect" {
						_, _ = io.Copy(io.Discard, r.Body)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					next.ServeHTTP(w, r)
				})
			})
			t.Cleanup(func() { close(release) })
			start, id := input(t, host, "startup")
			g.hook(host, "session-start", start, Options{Budget: 1500 * time.Millisecond})
			pending, e := g.association(host, id)
			if e != nil || pending.Status != "pending" {
				t.Fatalf("%+v %v", pending, e)
			}
			compact, _ := input(t, host, "compact")
			r := g.hook(host, "session-start", compact, Options{})
			wantContext(t, r, pending.Session, "lit workflow-session --host "+host+" --runtime-id "+id+" reconcile")
		})
	}
}

func TestCompactFailsSoftOnAnUnreadableAssociation(t *testing.T) {
	w := startWork(t, "claude")
	path, _, _ := workflowsession.Adapter{Host: "claude", RuntimeID: w.id}.Paths()
	if e := os.WriteFile(path, []byte("{broken"), 0o600); e != nil {
		t.Fatal(e)
	}
	r := w.compact(Options{})
	wantContext(t, r, "lit session setup failed")
	if strings.Count(r.Context, "\n") > 1 {
		t.Errorf("an error note is one line:\n%s", r.Context)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "{broken" {
		t.Fatal("the hook rewrote evidence")
	}
}

func TestCompactOutputMatchesTheCodexSchema(t *testing.T) {
	w := startWork(t, "codex")
	w.claimed("schema work", "checkpoint: ok")
	r := w.compact(Options{})
	schema := loadSchema(t, "session-start.output.schema.json")
	var out map[string]any
	if e := json.Unmarshal([]byte(r.Stdout), &out); e != nil {
		t.Fatal(e)
	}
	if problems := check(schema, schema, out, "output"); len(problems) != 0 {
		t.Fatalf("the schema rejects %s: %v", r.Stdout, problems)
	}
}

func TestExcerptKeepsTheStartAndTheEndOfALongComment(t *testing.T) {
	long := strings.Repeat("a", 100) + strings.Repeat("m", 1000) + strings.Repeat("z", 100)
	for name, tc := range map[string]struct {
		text       string
		head, tail int
		want       string
		cut        bool
	}{
		"short":       {"one two", 10, 5, "one two", false},
		"exact":       {"abcde", 3, 2, "abcde", false},
		"trimmed":     {"  padded \n", 20, 10, "padded", false},
		"cut":         {long, 100, 100, strings.Repeat("a", 100) + "\n[... 1000 characters omitted ...]\n" + strings.Repeat("z", 100), true},
		"multibyte":   {strings.Repeat("ü", 30), 5, 5, strings.Repeat("ü", 5) + "\n[... 20 characters omitted ...]\n" + strings.Repeat("ü", 5), true},
		"empty":       {" \n ", 5, 5, "", false},
		"no tail":     {"abcdefghij", 4, 0, "abcd\n[... 6 characters omitted ...]", true},
		"unix breaks": {"a\r\nb", 10, 10, "a\nb", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := excerpt(tc.text, tc.head, tc.tail); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTheRestoredContextStaysWithinWhatHostsAccept(t *testing.T) {
	long := strings.Repeat("🔥", 30000) // two UTF-16 units each, the worst case for the host's count
	v := restoreView{Session: "agent-x", Endpoint: "http://127.0.0.1:7411", Runtime: "r", Project: "feat/x (id)", Run: "lit workflow-session --host claude --runtime-id r run --"}
	for i := range 50 {
		issue := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		v.Claims = append(v.Claims, claimView{Issue: issue, Lease: "2026-10-14T01:46:06.88406748Z", Left: 59 * time.Minute,
			Comment: &comment{ID: issue, Author: "claude", At: "2026-10-14T00:46:06.682031Z", Body: long}, Shown: i < maxDetailedClaims})
	}
	text := v.String()
	if units := len(utf16.Encode([]rune(text))); units >= 10000 || !utf8.ValidString(text) {
		t.Fatalf("%d UTF-16 units, valid UTF-8 %v", units, utf8.ValidString(text))
	}
	for i, c := range v.Claims {
		if named := strings.Contains(text, c.Issue); named != (i < maxListedClaims) {
			t.Errorf("claim %d named: %v", i, named)
		}
	}
	if !strings.Contains(text, "and 30 more claims") {
		t.Errorf("the claims past the limit are counted:\n%s", text)
	}
}
