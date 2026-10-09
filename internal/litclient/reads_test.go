package litclient_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
)

func TestReadsUseTargetsQueriesCursorsAndLimits(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.connect("reads", "Human")
	project, issues := seed(t, s, "reads", "A", "B", "C")

	got, err := litclient.Get[protocol.Issue](ctx, s, project, "title:A", issues["B"].ID, "title:A")
	if err != nil || len(got) != 2 || got[0].Title != "A" || got[1].Title != "B" || got[0].Body != "A body" {
		t.Fatalf("targets: %+v %v", got, err)
	}
	if _, err = litclient.Get[protocol.Issue](ctx, s, project, "missing"); litclient.Code(err) != "not_found" {
		t.Fatalf("missing target: %v", err)
	}
	snapshots := h.count("POST /v1/snapshots")
	if _, err = litclient.Get[protocol.Issue](ctx, s, project); litclient.Code(err) != "invalid_arguments" || h.count("POST /v1/snapshots") != snapshots {
		t.Fatalf("no targets: %v", err)
	}

	first, err := litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: project, Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil || first.Items[0].Title != "A" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	rest, err := litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: project, Limit: 2, Cursor: *first.NextCursor})
	if err != nil || len(rest.Items) != 1 || rest.NextCursor != nil || rest.Items[0].Title != "C" {
		t.Fatalf("next page: %+v %v", rest, err)
	}
	all, err := litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: project, All: true, Sort: "title", Direction: "desc"})
	if err != nil || len(all.Items) != 3 || all.Items[0].Title != "C" {
		t.Fatalf("all: %+v %v", all, err)
	}
	filtered, err := litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: project, Title: "B"})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != issues["B"].ID {
		t.Fatalf("filtered: %+v %v", filtered, err)
	}
	projects, err := litclient.List[protocol.Project](ctx, s, litclient.Query{})
	if err != nil || len(projects.Items) != 1 || projects.Items[0].Title != project {
		t.Fatalf("projects: %+v %v", projects, err)
	}
	if _, err = litclient.List[protocol.Issue](ctx, s, litclient.Query{ProjectID: project, All: true, Limit: 1}); litclient.Code(err) != "validation_failed" {
		t.Fatalf("all with limit: %v", err)
	}

	// Errors keep their protocol code and details through wrapping.
	var pe *protocol.Error
	wrapped := fmt.Errorf("context: %w", err)
	if litclient.Code(wrapped) != "validation_failed" || !errors.As(wrapped, &pe) || pe.Details == nil {
		t.Fatalf("wrapped error: %v", wrapped)
	}
	if litclient.Code(nil) != "" || litclient.Code(errors.New("plain")) != "" {
		t.Fatal("non-protocol errors have no code")
	}
}

// Each typed operation runs under its own context and restores the session's.
func TestOperationsUseTheirOwnContext(t *testing.T) {
	h := newHarness(t)
	s := h.connect("contexts", "Human")
	project, _ := seed(t, s, "contexts", "A")
	base := s.Context
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := litclient.Get[protocol.Issue](cancelled, s, project, "A"); litclient.Code(err) != "service_unavailable" {
		t.Fatalf("cancelled read: %v", err)
	}
	if s.Context != base {
		t.Fatal("operation context leaked into the session")
	}
	if _, err := litclient.Get[protocol.Issue](context.Background(), s, project, "title:A"); err != nil {
		t.Fatalf("read after cancelled one: %v", err)
	}
}
