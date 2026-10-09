// Package audit implements the v1 audit sink (SDD §18.1): a JSONL file with
// 0600 permissions, atomic rotation bounded by max_file_bytes and max_files,
// a bounded queue with per-event acknowledgement, and fail-closed behavior:
// once the sink fails, every subsequent enqueue fails and no partial sink is
// used. Events are fully sanitized: no SQL, parameters, values, DSNs, or
// object names (SDD §18.1, PRD §14.4).
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ErrFailed is returned once the sink is broken; the process must fail closed.
var ErrFailed = fmt.Errorf("audit sink failed")

// Phase and Outcome constants used in the event schema.
const (
	PhaseAttempt = "attempt"
	PhaseOutcome = "outcome"
	OutcomeOK    = "success"
)

// Event is one sanitized audit event (SDD §18.1).
type Event struct {
	EventVersion int       `json:"event_version"`
	Timestamp    time.Time `json:"timestamp"`
	RequestID    string    `json:"request_id"`
	Phase        string    `json:"phase"`
	Tool         string    `json:"tool"`
	Connection   string    `json:"connection"`
	Outcome      string    `json:"outcome,omitempty"`
	ErrorCode    *string   `json:"error_code,omitempty"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
}

// Config mirrors the audit configuration block.
type Config struct {
	Path         string
	WriteTimeout time.Duration
	QueueSize    int
	MaxFileBytes int
	MaxFiles     int
}

// Writer appends JSONL to a 0600 file and rotates before exceeding
// max_file_bytes, keeping at most max_files rotated files. Any failure
// poisons the writer (fail-closed).
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	broken   error
}

// NewWriter opens the audit file in append mode with 0600 permissions.
func NewWriter(path string, maxBytes, maxFiles int) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Writer{path: path, maxBytes: int64(maxBytes), maxFiles: maxFiles, f: f}, nil
}

// Write appends one event line, rotating first when the current file is about
// to exceed max_file_bytes. Returns ErrFailed (and poisons the writer) on any
// I/O error; a partial sink is never used afterward.
func (w *Writer) Write(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken != nil {
		return w.broken
	}
	line, err := json.Marshal(ev)
	if err != nil {
		w.poison(fmt.Errorf("audit: marshal: %w", err))
		return w.broken
	}
	line = append(line, '\n')
	cur, err := w.f.Stat()
	if err != nil || cur.Size()+int64(len(line)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	if _, err := w.f.Write(line); err != nil {
		w.poison(fmt.Errorf("audit: write: %w", err))
		return w.broken
	}
	return nil
}

// rotate closes the active file, shifts the rotated chain, renames the active
// file to .1 and opens a fresh one; only after the new file is open is the
// oldest removed (SDD §18.1).
func (w *Writer) rotate() error {
	if err := w.f.Close(); err != nil {
		w.poison(fmt.Errorf("audit: close during rotate: %w", err))
		return w.broken
	}
	for i := w.maxFiles - 1; i >= 1; i-- {
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		src := fmt.Sprintf("%s.%d", w.path, i)
		if _, err := os.Stat(src); err == nil {
			if i+1 > w.maxFiles {
				continue
			}
			if err := os.Rename(src, dst); err != nil {
				w.poison(fmt.Errorf("audit: rotate rename %s: %w", src, err))
				return w.broken
			}
		}
	}
	if err := os.Rename(w.path, fmt.Sprintf("%s.1", w.path)); err != nil {
		w.poison(fmt.Errorf("audit: rotate active: %w", err))
		return w.broken
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.poison(fmt.Errorf("audit: open after rotate: %w", err))
		return w.broken
	}
	w.f = f
	oldest := fmt.Sprintf("%s.%d", w.path, w.maxFiles)
	if _, err := os.Stat(oldest); err == nil {
		_ = os.Remove(oldest)
	}
	return nil
}

func (w *Writer) poison(err error) {
	if w.broken == nil {
		w.broken = err
	}
	_ = w.f.Close()
}

// Close flushes and closes the active file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		return err
	}
	return nil
}

// Queue is a bounded async audit queue with per-event ack. The writer behind
// it runs on a dedicated goroutine; once the sink fails the queue is marked
// failed and every enqueue returns ErrFailed.
type Queue struct {
	cfg       Config
	w         *Writer
	ch        chan *item
	broken    atomic.Bool
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    chan struct{}
}

type item struct {
	ev   Event
	done chan error
}

// NewQueue opens the sink and starts the worker. A bad path fails startup so
// the process refuses to run without a working audit layer (SDD §18.1).
func NewQueue(cfg Config) (*Queue, error) {
	w, err := NewWriter(cfg.Path, cfg.MaxFileBytes, cfg.MaxFiles)
	if err != nil {
		return nil, fmt.Errorf("audit: open sink: %w", err)
	}
	q := &Queue{
		cfg:    cfg,
		w:      w,
		ch:     make(chan *item, cfg.QueueSize),
		closed: make(chan struct{}),
	}
	q.wg.Add(1)
	go q.worker()
	return q, nil
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for it := range q.ch {
		err := q.write(it.ev)
		it.done <- err
		close(it.done)
	}
}

func (q *Queue) write(ev Event) error {
	if err := q.w.Write(ev); err != nil {
		q.broken.Store(true)
		return ErrFailed
	}
	return nil
}

// Enqueue submits one event and waits for ack within ctx (SDD §17/§18.1). It
// returns ErrFailed once the sink is broken, or ctx's error when timing out.
func (q *Queue) Enqueue(ctx context.Context, ev Event) error {
	if q.broken.Load() {
		return ErrFailed
	}
	it := &item{ev: ev, done: make(chan error, 1)}
	select {
	case q.ch <- it:
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closed:
		return ErrFailed
	}
	select {
	case err := <-it.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-q.closed:
		return ErrFailed
	}
}

// Close drains the queue (waiting for in-flight writes) and closes the file.
func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		close(q.closed)
		close(q.ch)
		q.wg.Wait()
	})
	q.broken.Store(true)
	return q.w.Close()
}

// Failed reports whether the sink has failed irrecoverably.
func (q *Queue) Failed() bool { return q.broken.Load() }

// RotatePath is a helper for tests to locate a rotated file.
func RotatePath(base string, n int) string {
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("%s.%d", filepath.Base(base), n))
}
