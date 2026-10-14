package workflowsession

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestQueryTimedReturnsTheItemsAndTheServiceTime(t *testing.T) {
	adapterEnv(t)
	endpoint := testService(t, nil)
	ctx := context.Background()
	a := Adapter{Host: "claude", RuntimeID: "timed", Binary: litBinary(t), WorkDir: t.TempDir()}
	if _, e := a.Query(ctx, "session", "get"); e == nil {
		t.Fatal("a runtime without an association cannot be queried")
	}
	if _, e := a.Fresh(ctx, FreshOptions{Endpoint: endpoint}); e != nil {
		t.Fatal(e)
	}
	before := time.Now()
	items, at, e := a.QueryTimed(ctx, "session", "get")
	if e != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, e)
	}
	var runtime map[string]string
	if e = json.Unmarshal(items[0]["runtime"], &runtime); e != nil || runtime["session_id"] != "timed" {
		t.Fatalf("queried another session: %s", items[0]["runtime"])
	}
	if at.IsZero() || at.Before(before.Add(-time.Minute)) || at.After(time.Now().Add(time.Minute)) {
		t.Fatalf("service time %s is not now (%s)", at, before)
	}
	if none, at, e := a.QueryTimed(ctx, "claims", "list"); e != nil || len(none) != 0 || at.IsZero() {
		t.Fatalf("an empty answer still has a time: %v %s %v", none, at, e)
	}
	if _, _, e = a.QueryTimed(ctx, "issues", "get", "00000000-0000-4000-8000-000000000000"); e == nil {
		t.Fatal("a failing call must be an error")
	}
}
