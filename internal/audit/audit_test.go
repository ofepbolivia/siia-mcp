package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilePermissionsAndFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	q, err := NewQueue(Config{Path: path, WriteTimeout: time.Second, QueueSize: 16, MaxFileBytes: 4096, MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	code := "OBJECT_NOT_ALLOWED"
	if err := q.Enqueue(context.Background(), Event{
		EventVersion: 1, Timestamp: time.Now(), RequestID: "r1", Phase: PhaseOutcome,
		Tool: "db_query", Connection: "app_dev", Outcome: "error", ErrorCode: &code, DurationMS: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}
	line, err := readLine(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, line)
	}
	if ev.Tool != "db_query" || ev.RequestID != "r1" || ev.Outcome != "error" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.ErrorCode == nil || *ev.ErrorCode != code {
		t.Fatalf("error_code = %+v", ev.ErrorCode)
	}
}

func TestRotationKeepsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	q, err := NewQueue(Config{Path: path, WriteTimeout: time.Second, QueueSize: 16, MaxFileBytes: 60, MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := q.Enqueue(context.Background(), Event{
			EventVersion: 1, Timestamp: time.Now(), RequestID: "r", Phase: PhaseAttempt, Tool: "db_ping", Connection: "x",
		}); err != nil {
			// Several events may land in the same file before rotation; only
			// fail if the writer itself broke.
			q.Close()
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 4; n++ {
		if _, err := os.Stat(RotatePath(path, n)); err == nil {
			if n > 3 {
				t.Fatalf("%s should have been removed (max 3)", RotatePath(path, n))
			}
		}
	}
}

func TestFailClosedOnMissingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "audit.log") // parent does not exist
	if _, err := NewQueue(Config{Path: path, WriteTimeout: time.Second, QueueSize: 4, MaxFileBytes: 4096, MaxFiles: 3}); err == nil {
		t.Fatal("expected startup failure for unusable sink path")
	}
}

func TestSinkFailureFailsSubsequentEnqueues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	q, err := NewQueue(Config{Path: path, WriteTimeout: time.Second, QueueSize: 4, MaxFileBytes: 1, MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{EventVersion: 1, Timestamp: time.Now(), RequestID: "r", Phase: PhaseAttempt, Tool: "db_ping", Connection: "x"}

	// Close the underlying file so the next write deterministically poisons the
	// sink, including when tests run as root inside a release container.
	q.w.mu.Lock()
	if err := q.w.f.Close(); err != nil {
		q.w.mu.Unlock()
		t.Fatal(err)
	}
	q.w.mu.Unlock()
	if err := q.Enqueue(context.Background(), ev); err != nil && !errors.Is(err, ErrFailed) {
		t.Fatalf("unexpected error type: %v", err)
	}
	if err := q.Enqueue(context.Background(), ev); !errors.Is(err, ErrFailed) {
		t.Fatalf("post-failure enqueue = %v, want ErrFailed", err)
	}
	q.Close()
}

func readLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return "", sc.Err()
	}
	return strings.TrimSpace(sc.Text()), nil
}
