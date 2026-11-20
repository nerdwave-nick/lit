package workflowsession

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// RememberedEndpoint reports where Fresh will look for a remembered project
// without contacting anything or changing the preference.
func TestRememberedEndpointReadsTheSavedAddress(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	a := f.adapter("one")

	if got, e := a.RememberedEndpoint(dir); e != nil || got != "" {
		t.Fatalf("nothing remembered: %q %v", got, e)
	}
	saved := f.remember(dir, f.endpoint, "feat/alpha")
	before := readFile(t, f.preferenceFile(dir))
	requests := f.seen.count(contexts)

	got, e := a.RememberedEndpoint(dir)
	if e != nil || got != saved.Endpoint || got != f.endpoint {
		t.Fatalf("saved endpoint: %q %v", got, e)
	}
	if f.seen.count(contexts) != requests {
		t.Error("RememberedEndpoint must not contact the service")
	}
	if readFile(t, f.preferenceFile(dir)) != before {
		t.Error("RememberedEndpoint must not touch the preference")
	}
}

func TestRememberedEndpointRefusesAnUnreadablePreferenceLikeFresh(t *testing.T) {
	f := newAdoption(t)
	f.project(f.endpoint, "feat/alpha")
	dir := f.checkout()
	f.remember(dir, f.endpoint, "feat/alpha")
	if e := os.WriteFile(f.preferenceFile(dir), []byte("{broken"), 0o600); e != nil {
		t.Fatal(e)
	}

	_, e := f.adapter("one").RememberedEndpoint(dir)
	var refused *AdoptionError
	if !errors.As(e, &refused) || refused.Code != "invalid_local_state" || refused.Checkout != dir {
		t.Fatalf("want the AdoptionError Fresh returns: %v", e)
	}
	if readFile(t, f.preferenceFile(dir)) != "{broken" {
		t.Error("the damaged record must be left in place")
	}
	if _, e = f.adapter("one").RememberedEndpoint(filepath.Join(dir, "missing")); e == nil || errors.As(e, &refused) {
		t.Fatalf("a directory that does not exist is an error, but not a preference problem: %v", e)
	}
}

func TestAdoptionErrorRemedyOmitsTheAssurance(t *testing.T) {
	e := &AdoptionError{Recovery: "run 'lit context clear' to forget it; the remembered project was not changed"}
	if got := e.Remedy(); got != "run 'lit context clear' to forget it" {
		t.Fatalf("%q", got)
	}
	if got := (&AdoptionError{}).Remedy(); got != "" {
		t.Fatalf("%q", got)
	}
}
