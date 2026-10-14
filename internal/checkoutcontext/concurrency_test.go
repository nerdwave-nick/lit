package checkoutcontext_test

import (
	"bufio"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nerdwave-nick/lit/internal/checkoutcontext"
	"github.com/nerdwave-nick/lit/internal/litclient"
)

// The test binary doubles as the helper process: a mutation holding the lock
// of another process, and writers racing across processes.
const helperEnv = "LIT_CHECKOUTCONTEXT_TEST_HELPER"

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}
	args := strings.Split(os.Getenv(helperEnv+"_ARGS"), "\n")
	var err error
	switch mode {
	case "hold":
		err = holdLock(args[0])
	case "save":
		err = saveLoop(args[0], args[1], args[2])
	default:
		err = fmt.Errorf("unknown helper mode %q", mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func holdLock(lock string) error {
	unlock, err := litclient.AcquireLock(lock, time.Now().Add(10*time.Second))
	if err != nil {
		return err
	}
	defer unlock()
	fmt.Println("locked")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n') // until the parent closes stdin or kills us
	return nil
}

// saveLoop saves distinct preferences for the checkout and reads each back,
// requiring every read to be a complete record.
func saveLoop(state, dir, worker string) error {
	n, _ := strconv.Atoi(worker)
	c, err := checkoutcontext.Resolve(dir)
	if err != nil {
		return err
	}
	store := checkoutcontext.OpenAt(state)
	store.LockTimeout = 30 * time.Second
	for i := 0; i < 25; i++ {
		if _, err := store.Save(c, preference(n*100+i)); err != nil {
			return fmt.Errorf("save %d: %w", i, err)
		}
		if rec, err := store.Load(c); err != nil || rec == nil {
			return fmt.Errorf("load after save %d: %+v %v", i, rec, err)
		}
	}
	return nil
}

func helper(t *testing.T, mode string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", helperEnv+"="+mode, helperEnv+"_ARGS="+strings.Join(args, "\n"))
	return cmd
}

func TestConcurrentWritersAndReadersSeeOnlyCompleteRecords(t *testing.T) {
	state, c := physical(t), plain(t)
	store := checkoutcontext.OpenAt(state)
	store.LockTimeout = 30 * time.Second
	if _, err := store.Save(c, preference(0)); err != nil {
		t.Fatal(err)
	}
	const writers, saves = 6, 25
	valid := map[checkoutcontext.Preference]bool{preference(0): true}
	for w := 1; w <= writers; w++ {
		for i := 0; i < saves; i++ {
			valid[preference(w*100+i)] = true
		}
	}
	var done atomic.Bool
	var wg, readers sync.WaitGroup
	failures := make(chan string, 64)
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !done.Load() {
				rec, err := checkoutcontext.OpenAt(state).Load(c)
				if err != nil || rec == nil || !valid[rec.Preference] {
					failures <- fmt.Sprintf("reader saw %+v, %v", rec, err)
					return
				}
			}
		}()
	}
	for w := 1; w <= writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < saves; i++ {
				if _, err := store.Save(c, preference(w*100+i)); err != nil {
					failures <- fmt.Sprintf("writer %d save %d: %v", w, i, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	done.Store(true)
	readers.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	rec, err := store.Load(c)
	if err != nil || rec == nil || !valid[rec.Preference] {
		t.Fatalf("final record %+v, %v", rec, err)
	}
	if pending, _ := os.ReadDir(recordDir(state)); len(pending) != 2 {
		var names []string
		for _, e := range pending {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only the record and its lock", names)
	}
}

func TestConcurrentProcessesNeverTearTheRecord(t *testing.T) {
	state, c := physical(t), plain(t)
	var cmds []*exec.Cmd
	var outputs []*strings.Builder
	for w := 1; w <= 4; w++ {
		cmd := helper(t, "save", state, c.Path, strconv.Itoa(w))
		out := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, outputs = append(cmds, cmd), append(outputs, out)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("writer process %d: %v\n%s", i+1, err, outputs[i])
		}
	}
	rec, err := checkoutcontext.OpenAt(state).Load(c)
	if err != nil || rec == nil {
		t.Fatalf("final record %+v, %v", rec, err)
	}
	n := 0
	for w := 1; w <= 4; w++ {
		if rec.Preference == preference(w*100+24) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("final record %+v is not the last save of any writer", rec.Preference)
	}
}

// holder starts a process holding the checkout's lock; closing the returned
// pipe (or killing the process) releases it.
func holder(t *testing.T, state string, c checkoutcontext.Checkout) (release func(), kill func()) {
	t.Helper()
	// A first mutation creates the lock file the way the tool does.
	if _, err := checkoutcontext.OpenAt(state).Save(c, preference(1)); err != nil {
		t.Fatal(err)
	}
	cmd := helper(t, "hold", lockFile(state, c))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("holder says %q, %v", line, err)
	}
	return func() { _ = stdin.Close(); _ = cmd.Wait() }, func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

func TestAMutationHeldByAnotherProcessTimesOutAsLocked(t *testing.T) {
	state, c := physical(t), plain(t)
	release, _ := holder(t, state, c)
	store := checkoutcontext.OpenAt(state)
	store.LockTimeout = 100 * time.Millisecond
	before := snapshot(t, state)

	start := time.Now()
	if _, err := store.Save(c, preference(2)); !errors.Is(err, checkoutcontext.ErrLocked) || time.Since(start) < 80*time.Millisecond {
		t.Fatalf("Save = %v after %s, want ErrLocked after the timeout", err, time.Since(start))
	}
	if _, err := store.Clear(c); !errors.Is(err, checkoutcontext.ErrLocked) {
		t.Fatalf("Clear = %v, want ErrLocked", err)
	}
	// Readers are never blocked: they see the complete record.
	if rec, err := store.Load(c); err != nil || rec == nil || rec.Preference != preference(1) {
		t.Fatalf("Load during the lock = %+v, %v", rec, err)
	}
	if after := snapshot(t, state); !maps.Equal(before, after) {
		t.Fatalf("a refused mutation changed the state")
	}
	release()
	if _, err := store.Save(c, preference(2)); err != nil {
		t.Fatalf("Save after release: %v", err)
	}
}

func TestAMutationWaitsForTheLockAndThenWins(t *testing.T) {
	state, c := physical(t), plain(t)
	release, _ := holder(t, state, c)
	store := checkoutcontext.OpenAt(state)
	store.LockTimeout = 20 * time.Second
	result := make(chan error, 1)
	go func() { _, err := store.Save(c, preference(2)); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("Save finished while another process held the lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if rec, err := store.Load(c); err != nil || rec.Preference != preference(1) {
		t.Fatalf("Load while waiting = %+v, %v", rec, err)
	}
	release()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if rec, err := store.Load(c); err != nil || rec.Preference != preference(2) {
		t.Fatalf("after the wait Load = %+v, %v", rec, err)
	}
}

func TestALockDiesWithItsProcess(t *testing.T) {
	state, c := physical(t), plain(t)
	_, kill := holder(t, state, c)
	kill() // SIGKILL: no cleanup runs
	store := checkoutcontext.OpenAt(state)
	store.LockTimeout = 5 * time.Second
	if _, err := store.Save(c, preference(2)); err != nil {
		t.Fatalf("Save after the holder was killed: %v", err)
	}
}
