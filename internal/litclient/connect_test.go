package litclient_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/litclient"
	"github.com/nerdwave-nick/lit/internal/protocol"
)

func TestConnectRegistersAFreshClientAndDisconnectReleasesItsClaims(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := h.open("fresh")
	if s.Mapping.ClientID != "" {
		t.Fatalf("new session is mapped: %+v", s.Mapping)
	}
	actor := protocol.Actor{Name: "Nick", Kind: "human"}
	if err := s.Connect(ctx, litclient.Registration{Actor: actor, Runtime: protocol.Runtime{Vendor: "lit-tui", SessionID: "instance-1"}}); err != nil {
		t.Fatal(err)
	}
	if s.Client.Status != "connected" || s.Client.Actor != actor || s.Client.Runtime.Vendor != "lit-tui" || s.Mapping.ClientID != s.Client.ClientID || s.Mapping.ServiceID != s.Meta.ServiceID || s.Mapping.Endpoint != h.url {
		t.Fatalf("connected session: %+v %+v", s.Client, s.Mapping)
	}
	if m, err := litclient.ReadMapping(litclient.MappingPath(h.stateDir, "fresh")); err != nil || m != s.Mapping {
		t.Fatalf("saved mapping: %+v %v", m, err)
	}
	if files := h.pending(); len(files) != 0 {
		t.Fatalf("confirmed connect kept evidence: %v", files)
	}
	connects := h.count("POST /v1/connect")
	if err := s.Connect(ctx, litclient.Registration{Actor: actor}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("connect of a mapped session: %v", err)
	}
	unlocked := &litclient.Session{StateDir: h.stateDir, HTTP: http.DefaultClient, Context: ctx}
	if err := unlocked.Open(litclient.OpenOptions{Endpoint: h.url, EndpointSet: true, AllowUnmapped: true}); err != nil {
		t.Fatal(err)
	}
	if err := unlocked.Connect(ctx, litclient.Registration{Actor: actor}); litclient.Code(err) != "invalid_arguments" {
		t.Fatalf("connect without the mapping lock: %v", err)
	}
	if h.count("POST /v1/connect") != connects {
		t.Fatal("refused registrations were sent")
	}

	project, issues := seed(t, s, "lifecycle", "A")
	selected := h.open("selected")
	if err := selected.Connect(ctx, litclient.Registration{Actor: actor, Project: project}); err != nil || selected.Client.ProjectID == nil {
		t.Fatalf("connect with project: %+v %v", selected.Client, err)
	}
	if _, err := s.AcquireClaims(ctx, litclient.ClaimRequest{ClaimSelection: litclient.ClaimSelection{Project: project, Issues: []string{issues["A"].ID}}}); err != nil {
		t.Fatal(err)
	}
	outcome, err := s.Disconnect(ctx)
	if err != nil || outcome != "applied" || s.Client.Status != "disconnected" {
		t.Fatalf("disconnect: %q %+v %v", outcome, s.Client, err)
	}
	if snap, err := selected.SnapshotClaims(ctx, litclient.ClaimSelection{Issues: []string{issues["A"].ID}}); err != nil || snap.Items[0].Claim != nil {
		t.Fatalf("claim after disconnect: %+v %v", snap, err)
	}
	if outcome, err = s.Disconnect(ctx); err != nil || outcome != "already_disconnected" {
		t.Fatalf("second disconnect: %q %v", outcome, err)
	}
	s.Close()
	again := &litclient.Session{StateDir: h.stateDir, HTTP: http.DefaultClient, Context: ctx}
	if err := again.Lock("fresh", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := again.Open(litclient.OpenOptions{Endpoint: h.url, EndpointSet: true}); litclient.Code(err) != "client_disconnected" {
		t.Fatalf("reopening a disconnected session: %v", err)
	}
}

// The workflow stage is part of the client record a reopened session reads, so
// a status line built on litclient sees what the session's skills recorded.
func TestOpenedSessionExposesTheClientsWorkflowStage(t *testing.T) {
	h := newHarness(t)
	setter := h.connect("staged", "Agent")
	if setter.Client.Stage != nil {
		t.Fatalf("a new client has stage %q", *setter.Client.Stage)
	}
	// A second view of the same logical session, as a status line has.
	reader := func() *litclient.Session {
		t.Helper()
		s := &litclient.Session{StateDir: h.stateDir, HTTP: http.DefaultClient, Context: context.Background(), Mapping: setter.Mapping}
		if err := s.Open(litclient.OpenOptions{Endpoint: h.url, EndpointSet: true}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	setStage := func(stage any) {
		t.Helper()
		revision := fmt.Sprintf("\"client:%d\"", setter.Client.StateRevision)
		if err := setter.Call("PATCH", "/v1/clients/"+setter.Client.ClientID, map[string]any{"stage": stage}, map[string]string{"If-Match": revision}, &setter.Client, true); err != nil {
			t.Fatal(err)
		}
	}
	if s := reader(); s.Client.Stage != nil {
		t.Fatalf("stage before any is set: %q", *s.Client.Stage)
	}
	setStage("to-tickets")
	if s := reader(); s.Client.Stage == nil || *s.Client.Stage != "to-tickets" {
		t.Fatalf("stage after set: %v", s.Client.Stage)
	}
	setStage(nil)
	if s := reader(); s.Client.Stage != nil {
		t.Fatalf("stage after clear: %q", *s.Client.Stage)
	}
}
