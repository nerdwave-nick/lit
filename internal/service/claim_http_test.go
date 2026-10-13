package service

import (
	"fmt"
	"github.com/nerdwave-nick/lit/internal/protocol"
	"sync"
	"testing"
	"time"
)

func TestClaimConcurrentAcquireAndBoundedResponse(t *testing.T) {
	s, c, id := claimFixture(t)
	clients := []protocol.Client{c, c}
	clients[1].ClientID = protocol.UUID()
	if e := s.SaveClient(clients[1]); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, client := range clients {
		wg.Add(1)
		go func(c protocol.Client) {
			defer wg.Done()
			code, _ := directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim", map[string]any{})
			codes <- code
		}(client)
	}
	wg.Wait()
	close(codes)
	wins, conflicts := 0, 0
	for code := range codes {
		switch code {
		case 201:
			wins++
		case 409:
			conflicts++
		default:
			t.Fatal(code)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	claim, _ := s.LiveClaim(id)
	before := *claim
	s.Config.Limits.SnapshotBytes = 100
	owner := c
	if owner.ClientID != claim.OwnerClientID {
		owner = clients[1]
	}
	code, _ := directRequest(t, s, owner, "POST", "/v1/issues/"+id+"/claim/renew", map[string]any{"token": claim.Token, "extend_to": time.Now().UTC().Add(50 * time.Minute).Format(time.RFC3339Nano)})
	if code != 413 {
		t.Fatal(code)
	}
	after, _ := s.LiveClaim(id)
	if !same(before, *after) {
		t.Fatal("bounded response changed claim")
	}
}
func TestClaimMalformedInputsAndNullRead(t *testing.T) {
	s, c, id := claimFixture(t)
	code, v := directRequest(t, s, c, "GET", "/v1/issues/"+id+"/claim", nil)
	if code != 200 {
		t.Fatal(code)
	}
	if value, present := v["data"]; !present || value != nil {
		t.Fatal(v)
	}
	for _, payload := range []map[string]any{{"extend_to": nil}, {"force": nil}, {"token": ""}, {"extra": true}, {"extend_to": "yesterday"}} {
		code, _ := directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim", payload)
		if code < 400 || code >= 500 {
			t.Fatal(payload, code)
		}
	}
	for _, item := range []map[string]any{{"issue_id": id, "force": false}, {"issue_id": id, "token": nil}, {"issue_id": id, "token": "bad"}} {
		body := map[string]any{"operation": "claims.renew", "owner_client_id": c.ClientID, "selection": "explicit", "items": []any{item}}
		code, _ := directRequest(t, s, c, "POST", "/v1/operations", body)
		if code < 400 || code >= 500 {
			t.Fatal(body, code)
		}
	}
	code, v = directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim", map[string]any{})
	if code != 201 {
		t.Fatal(code, v)
	}
	claim, _ := s.LiveClaim(id)
	for _, path := range []string{"/v1/claims?owner_client_id=" + c.ClientID, "/v1/claim-snapshots"} {
		method := "GET"
		var body any
		if path == "/v1/claim-snapshots" {
			method = "POST"
			body = map[string]any{"owner_client_id": c.ClientID}
		}
		code, v = directRequest(t, s, c, method, path, body)
		if code != 200 {
			t.Fatal(path, code, v)
		}
	}
	// Owner-wide empty selection must not silently drop the live claim.
	code, v = directRequest(t, s, c, "POST", "/v1/operations", protocol.ClaimOperation{Operation: "claims.release", OwnerClientID: c.ClientID, Selection: "all_owned", Items: []protocol.ClaimItem{}})
	if code != 409 || fmt.Sprint(v) == "" {
		t.Fatal(code, v)
	}
	if current, _ := s.LiveClaim(id); current.Token != claim.Token {
		t.Fatal(current)
	}
}

func TestClaimAcquisitionCapsWhileRenewalRejectsOverlongTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   time.Duration
	}{
		{"default", 0, 30 * time.Minute},
		{"exact one hour", time.Hour, time.Hour},
		{"beyond one hour", time.Hour + time.Nanosecond, time.Hour},
		{"far future", 3 * time.Hour, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, id := claimFixture(t)
			now := time.Now().UTC()
			s.claimClock = func() time.Time { return now }
			payload := map[string]any{}
			if tc.offset > 0 {
				payload["extend_to"] = now.Add(tc.offset).Format(time.RFC3339Nano)
			}
			code, v := directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim", payload)
			if code != 201 {
				t.Fatal(code, v)
			}
			got, e := s.LiveClaim(id)
			if e != nil {
				t.Fatal(e)
			}
			if got.ExpiresAt != now.Add(tc.want).Format(time.RFC3339Nano) {
				t.Fatal("incorrect grant cap", got)
			}
			original := *got
			// Acquisitions never replace an existing live token on retry, even though
			// the original excessive target would be capped against a later clock.
			now = now.Add(time.Minute)
			code, _ = directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim", payload)
			if code != 409 {
				t.Fatal("acquire retry replaced claim", code)
			}
			code, _ = directRequest(t, s, c, "POST", "/v1/issues/"+id+"/claim/renew", map[string]any{"token": got.Token, "extend_to": now.Add(time.Hour + time.Nanosecond).Format(time.RFC3339Nano)})
			if code != 422 {
				t.Fatal("excessive renewal accepted", code)
			}
			current, e := s.LiveClaim(id)
			if e != nil || !same(*current, original) {
				t.Fatal("rejection changed claim", current, e)
			}
		})
	}
}
