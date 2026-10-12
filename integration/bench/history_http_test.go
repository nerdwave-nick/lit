package bench

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/service"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/storage"
)

// historyServer is an in-process service holding one long-lived project of
// the benchmark corpus (ProjectShape(10)), seeded as the harness seeds it
// through preview and commit. It returns the server, a connected client and
// the long-lived issue, which has over 200 revisions. The text comes from the Go
// toolchain's net/http sources rather than this repository's, so that edits
// here do not change the corpus between measurements.
func historyServer(tb testing.TB) (*service.Server, protocol.Client, string) {
	tb.Helper()
	if runtime.GOROOT() == "" {
		tb.Skip("no GOROOT sources for the text pool")
	}
	pool, err := LoadTextPool(filepath.Join(runtime.GOROOT(), "src", "net", "http"))
	if err != nil {
		tb.Fatal(err)
	}
	db, err := storage.OpenWith(tb.TempDir(), storage.Options{})
	if err != nil {
		tb.Fatal(err)
	}
	s, err := service.New(db, service.Config{Limits: protocol.DefaultLimits(), TitlePrefixes: []string{"feat"}})
	if err != nil {
		db.Close()
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.DB.Close() })
	c := protocol.Client{SchemaVersion: 1, ClientID: protocol.UUID(), StateRevision: 1, Status: "connected", Actor: protocol.Actor{Name: "bench", Kind: "agent"}}
	if err = s.SaveClient(c); err != nil {
		tb.Fatal(err)
	}
	plan := PlanProject(pool, 1, 10, ProjectShape(10))
	for _, t := range plan.Txns {
		var prepared protocol.Prepared
		serveJSON(tb, s, c, "POST", "/v1/transaction-previews", protocol.PrepareRequest{SchemaVersion: 1, Operation: t.Operation, Project: t.Project, Items: t.Items}, &prepared)
		var result protocol.MutationResult
		serveJSON(tb, s, c, "POST", "/v1/transactions", protocol.DurableRequest{SchemaVersion: 1, Intent: prepared.Intent, RequestHash: prepared.RequestHash}, &result)
		if result.Outcome != "applied" {
			tb.Fatal(t.Operation, result.Outcome)
		}
	}
	for _, st := range plan.Issues {
		if st.LongLived {
			return s, c, st.ID
		}
	}
	tb.Fatal("no long-lived issue")
	return nil, c, ""
}

// serveJSON serves one request through the service handler and decodes the
// response envelope's data into out.
func serveJSON(tb testing.TB, s *service.Server, c protocol.Client, method, path string, body, out any) {
	tb.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		tb.Fatal(err)
	}
	w := serve(s, c, method, path, b)
	var env struct{ Data json.RawMessage }
	if err = json.Unmarshal(w.Body.Bytes(), &env); err != nil || w.Code >= 300 {
		tb.Fatal(method, path, w.Code, err, w.Body.String())
	}
	if err = json.Unmarshal(env.Data, out); err != nil {
		tb.Fatal(err)
	}
}

func serve(s *service.Server, c protocol.Client, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// discard is a response writer that keeps only the status and the length,
// as a socket would.
type discard struct {
	header http.Header
	code   int
	n      int
}

func (d *discard) Header() http.Header         { return d.header }
func (d *discard) WriteHeader(code int)        { d.code = code }
func (d *discard) Write(p []byte) (int, error) { d.n += len(p); return len(p), nil }

// BenchmarkHistoryHTTP205 serves the harness's history.200 request (the
// whole history of the corpus's long-lived issue in one read, GET
// /v1/issues/<id>/history?all=true) through the service handler. "decoded"
// also decodes the response as the harness's client does, which the
// harness's latency includes.
func BenchmarkHistoryHTTP205(b *testing.B) {
	s, c, issue := historyServer(b)
	r := httptest.NewRequest("GET", "/v1/issues/"+issue+"/history?all=true", nil)
	r.Header.Set("X-Lit-Client-ID", c.ClientID)
	b.Run("served", func(b *testing.B) {
		var w *discard
		for b.Loop() {
			w = &discard{header: http.Header{}, code: 200}
			s.ServeHTTP(w, r)
			if w.code != 200 {
				b.Fatal(w.code)
			}
		}
		b.ReportMetric(float64(w.n), "response-bytes")
	})
	b.Run("decoded", func(b *testing.B) {
		var items int
		for b.Loop() {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			var env envelope
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || w.Code != 200 {
				b.Fatal(w.Code, err)
			}
			items = len(env.Items)
		}
		b.ReportMetric(float64(items), "entries")
	})
}
