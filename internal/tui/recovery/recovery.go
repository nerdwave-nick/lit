// Package recovery cleans up after lit-tui instances that ended without an
// orderly exit (SIGKILL, power loss, a crash). At launch it scans the
// per-instance registry, following every page, and never touches an entry
// whose liveness lock is held: that instance is alive. For an orphan it
// inspects the retained requests first. Only when none is uncertain does it
// disconnect the orphan's client, which releases its leftover claims, and
// remove the entry. Otherwise nothing is disconnected and the request IDs and
// evidence paths are reported for the user to decide. A disconnect that cannot
// be confirmed is reported as unknown, never as clean. When advisory locks in
// the registry cannot be trusted, orphans are reported for the user to
// confirm instead. Evidence is never removed, moved or replayed.
package recovery

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nerdwave-nick/nerdwave-workflows/internal/litclient"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/protocol"
	"github.com/nerdwave-nick/nerdwave-workflows/internal/tui/registry"
)

// Outcome is what recovery did with an orphan.
type Outcome string

const (
	// Disconnected: its client was disconnected (or already was), releasing its
	// claims, and its entry was removed.
	Disconnected Outcome = "disconnected"
	// Uncertain: retained requests are uncertain; nothing was disconnected and
	// the entry is kept.
	Uncertain Outcome = "uncertain"
	// Unknown: the outcome could not be verified (the service was unreachable,
	// refused, or did not confirm the disconnect); the entry is kept unless
	// Removed says otherwise.
	Unknown Outcome = "unknown"
	// Ask: the lock cannot be trusted to show the instance has gone; nothing was
	// done and the user decides (Recover).
	Ask Outcome = "ask"
)

// Orphan is one abandoned instance and what recovery did with it.
type Orphan struct {
	Entry    registry.Entry
	Info     registry.Info
	ClientID string
	Outcome  Outcome
	// Requests are the uncertain requests (Uncertain).
	Requests []Request
	// Reason explains Unknown and Ask.
	Reason string
	// Removed reports that the entry was removed.
	Removed bool
}

// Report is the result of one registry scan.
type Report struct {
	// Live counts entries whose lock is held: other running instances.
	Live    int
	Orphans []Orphan
}

// DefaultTimeout bounds the recovery of one orphan.
const DefaultTimeout = 10 * time.Second

// Options configure recovery.
type Options struct {
	StateDir string
	// Self is this instance's ID; its own entry is skipped.
	Self string
	HTTP *http.Client
	// Host is this machine's host name. An orphan started on another host
	// cannot be proven gone from here, so it is asked about.
	Host string
	// Trust reports whether advisory locks in the registry directory can be
	// trusted; nil uses registry.LockTrust.
	Trust func(dir string) (bool, string)
	// Do runs the recovery of one orphan with a bounded context, for example
	// under the lifecycle's mutation gate so shutdown waits for it. An error
	// (shutdown has started) stops the scan. Nil runs it with Timeout.
	Do func(fn func(ctx context.Context) error) error
	// Timeout bounds one orphan when Do is nil; zero is DefaultTimeout.
	Timeout time.Duration
	// Page is how many registry directory entries are read at a time.
	Page int
}

// Scan recovers every orphan in the registry. It stops early with an error
// only when Do refuses to run (shutdown has started) or the registry cannot
// be listed.
func Scan(o Options) (Report, error) {
	var r Report
	entries, err := registry.Scan(o.StateDir, o.Page)
	if err != nil {
		return r, err
	}
	trust := o.Trust
	if trust == nil {
		trust = registry.LockTrust
	}
	why := ""
	if trusted, fs := trust(registry.Root(o.StateDir)); !trusted {
		why = "advisory locks on " + fs + " cannot prove that the instance has gone"
	}
	for _, e := range entries {
		if e.ID == o.Self {
			continue
		}
		var orphan Orphan
		var st state
		if err := o.do(func(ctx context.Context) error { orphan, st = recoverEntry(ctx, o, e, why); return nil }); err != nil {
			return r, err
		}
		switch st {
		case live:
			r.Live++
		case abandoned:
			r.Orphans = append(r.Orphans, orphan)
		}
	}
	return r, nil
}

// Recover recovers one entry the user confirmed after Scan returned it with
// Ask: its free lock is trusted. ok is false when the entry has a live owner
// or is gone.
func Recover(o Options, e registry.Entry) (orphan Orphan, ok bool, err error) {
	var st state
	err = o.do(func(ctx context.Context) error {
		o.Host = ""
		orphan, st = recoverEntry(ctx, o, e, "")
		return nil
	})
	return orphan, err == nil && st == abandoned, err
}

func (o Options) do(fn func(ctx context.Context) error) error {
	if o.Do != nil {
		return o.Do(fn)
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx)
}

type state int

const (
	abandoned state = iota
	live
	gone
)

// recoverEntry recovers one entry while holding its liveness lock. A held
// lock means a live instance. untrusted, when not empty, explains why a free
// lock does not prove the instance has gone: the user is asked instead.
func recoverEntry(ctx context.Context, o Options, e registry.Entry, untrusted string) (Orphan, state) {
	orphan := Orphan{Entry: e}
	s := &litclient.Session{StateDir: o.StateDir, HTTP: o.HTTP, Context: ctx}
	if s.HTTP == nil {
		s.HTTP = http.DefaultClient
	}
	if err := s.LockPath(e.SessionPath(), time.Now()); err != nil {
		if litclient.Code(err) == "session_locked" {
			return orphan, live
		}
		orphan.Outcome, orphan.Reason = Unknown, "unreadable session mapping: "+err.Error()
		return orphan, abandoned
	}
	unlock := s.Close
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	info, err := registry.ReadInfo(e)
	if os.IsNotExist(err) {
		return orphan, gone // removed by its owner or another recovery meanwhile
	}
	orphan.Info, orphan.ClientID = info, s.Mapping.ClientID
	if err != nil {
		orphan.Outcome, orphan.Reason = Unknown, err.Error()
		return orphan, abandoned
	}
	switch {
	case untrusted != "":
		orphan.Outcome, orphan.Reason = Ask, untrusted
		return orphan, abandoned
	case o.Host != "" && info.Host != "" && info.Host != o.Host:
		orphan.Outcome, orphan.Reason = Ask, "it was started on "+info.Host+", so its lock cannot be checked from here"
		return orphan, abandoned
	}
	mapped := s.Mapping.ClientID != ""
	if mapped {
		if err := s.Open(litclient.OpenOptions{AllowDisconnected: true}); err != nil {
			orphan.Outcome, orphan.Reason = Unknown, describe(err)
			return orphan, abandoned
		}
	}
	// Check the retained requests first: disconnecting a client with an
	// uncertain request can make its outcome permanently unprovable.
	requests, err := Inspect(ctx, s, info.InstanceID)
	switch {
	case err != nil:
		orphan.Outcome, orphan.Reason = Unknown, "its retained requests could not be checked: "+describe(err)
		return orphan, abandoned
	case len(requests) > 0:
		orphan.Outcome, orphan.Requests = Uncertain, requests
		return orphan, abandoned
	case !mapped:
		// Nothing proves whether it registered a client: it ended before its
		// registration was sent, or after it was confirmed but before its
		// mapping was saved. Nothing can verify that later either.
		orphan.Outcome, orphan.Reason = Unknown, "its registration was never confirmed; a client it may have created stays connected"
	case s.Client.Status == "disconnected":
		orphan.Outcome = Disconnected
	default:
		if _, err := s.Disconnect(ctx); err != nil {
			orphan.Outcome, orphan.Reason = Unknown, "disconnect not confirmed: "+describe(err)
			if id := requestID(err); id != "" {
				orphan.Outcome = Uncertain
				orphan.Requests = []Request{{ID: id, Status: "uncertain", Proof: litclient.Code(err), Path: filepath.Join(o.StateDir, "pending", id+".json")}}
			}
			return orphan, abandoned
		}
		orphan.Outcome = Disconnected
	}
	if err := registry.Remove(e, unlock); err != nil {
		orphan.Reason = "its registry entry could not be removed: " + err.Error()
	} else {
		orphan.Removed = true
	}
	unlock = nil
	return orphan, abandoned
}

// describe is an error's code and message for the user.
func describe(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe.Code + ": " + pe.Message
	}
	return err.Error()
}

// requestID is the retained request of an uncertain mutation.
func requestID(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.Code == "outcome_uncertain" {
		if d, ok := pe.Details.(map[string]any); ok {
			id, _ := d["request_id"].(string)
			return id
		}
	}
	return ""
}
