package bench

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nerdwave-nick/lit/internal/storage"
)

// Server is a lit-server process on a temporary data directory and a free
// loopback port. It never touches the user's tracker: the configuration file,
// data directory and port are explicit, and inherited LIT_* variables are
// dropped.
type Server struct {
	Bin, Data, Config, Endpoint string
	cmd                         *exec.Cmd
	exited                      chan error
	log                         *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// WriteConfig writes the benchmark's lit-server configuration into dir: the
// defaults, with scheduled backups off so no snapshot runs during a
// measurement.
func WriteConfig(dir string) (string, error) {
	path := filepath.Join(dir, "config.json")
	return path, os.WriteFile(path, []byte(`{"schema_version":1,"backup_interval":"0s"}`+"\n"), 0o600)
}

// StartServer starts bin and returns once GET /v1/meta answers 200. The
// returned duration runs from starting the process to that answer, so it
// includes opening the database and, after an unclean shutdown, the full
// integrity scan.
func StartServer(bin, data, config string) (*Server, time.Duration, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	addr := l.Addr().String()
	_ = l.Close()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LIT_") {
			env = append(env, kv)
		}
	}
	s := &Server{Bin: bin, Data: data, Config: config, Endpoint: "http://" + addr, log: &syncBuffer{}}
	s.cmd = exec.Command(bin, "--config", config, "--data-dir", data, "--listen", addr)
	s.cmd.Env = env
	s.cmd.Stderr = s.log
	exited := make(chan error, 1)
	start := time.Now()
	if err = s.cmd.Start(); err != nil {
		return nil, 0, err
	}
	go func() { exited <- s.cmd.Wait() }()
	probe := &http.Client{Timeout: time.Second}
	for deadline := start.Add(5 * time.Minute); time.Now().Before(deadline); {
		select {
		case err = <-exited:
			return nil, 0, fmt.Errorf("lit-server exited during startup: %v: %s", err, s.log)
		default:
		}
		if r, err := probe.Get(s.Endpoint + "/v1/meta"); err == nil {
			_ = r.Body.Close()
			if r.StatusCode == 200 {
				s.exited = exited
				return s, time.Since(start), nil
			}
			_ = s.cmd.Process.Kill()
			return nil, 0, fmt.Errorf("lit-server answered %d at startup (degraded mode?): %s", r.StatusCode, s.log)
		}
		time.Sleep(2 * time.Millisecond)
	}
	_ = s.cmd.Process.Kill()
	return nil, 0, fmt.Errorf("lit-server did not answer within 5 minutes: %s", s.log)
}

// Stop interrupts the server and waits for it to drain and exit.
func (s *Server) Stop() error {
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	select {
	case err := <-s.exited:
		if err != nil {
			return fmt.Errorf("lit-server stop: %v: %s", err, s.log)
		}
		return nil
	case <-time.After(2 * time.Minute):
		_ = s.cmd.Process.Kill()
		return fmt.Errorf("lit-server did not stop: %s", s.log)
	}
}

// Kill kills the server (kill -9), as a crash would end it, and waits for it
// to exit.
func (s *Server) Kill() error {
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-s.exited:
		return nil
	case <-time.After(time.Minute):
		return fmt.Errorf("lit-server did not exit after kill -9: %s", s.log)
	}
}

// Log is the server's standard error so far.
func (s *Server) Log() string { return s.log.String() }

// DatabaseSize is the size of the database file and its WAL in data.
func DatabaseSize(data string) (db, wal int64) {
	if fi, err := os.Stat(filepath.Join(data, storage.DatabaseFile)); err == nil {
		db = fi.Size()
	}
	if fi, err := os.Stat(filepath.Join(data, storage.DatabaseFile+"-wal")); err == nil {
		wal = fi.Size()
	}
	return db, wal
}
