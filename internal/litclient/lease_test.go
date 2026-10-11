package litclient

import (
	"testing"
	"time"
)

// Moved from internal/cli (TestClaimTargetUsesServiceTime) with the claim
// lease computation it covers.
func TestClaimTargetUsesServiceTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, e := leaseTarget(now.Format(time.RFC3339Nano), 0, nil)
	if e != nil || got != now.Add(30*time.Minute).Format(time.RFC3339Nano) {
		t.Fatal(got, e)
	}
	for _, target := range []time.Time{now, now.Add(time.Hour + time.Second)} {
		if _, e = leaseTarget(now.Format(time.RFC3339Nano), 0, &target); e == nil {
			t.Fatal("accepted", target)
		}
	}
}
