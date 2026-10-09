package build

import (
	"os"
	"sync"
	"time"
)

const outputReadIdleTimeout = 5 * time.Second

type commandOutputReader struct {
	pipe          *os.File
	idleTimeout   time.Duration
	mu            sync.Mutex
	processExited bool
	reading       bool
	readSequence  uint64
	timedOut      bool
	timer         *time.Timer
}

func (r *commandOutputReader) Read(buffer []byte) (int, error) {
	r.mu.Lock()
	r.reading = true
	r.readSequence++
	if r.processExited {
		r.startReadTimerLocked()
	}
	r.mu.Unlock()

	n, err := r.pipe.Read(buffer)

	r.mu.Lock()
	r.reading = false
	if r.timer != nil {
		r.timer.Stop()
	}
	timedOut := r.timedOut
	r.mu.Unlock()
	if timedOut {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

func (r *commandOutputReader) markProcessExited() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processExited = true
	if r.reading {
		r.startReadTimerLocked()
	}
}

func (r *commandOutputReader) startReadTimerLocked() {
	sequence := r.readSequence
	r.timer = time.AfterFunc(r.idleTimeout, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		// A stopped timer can already be waiting for this lock during a later read.
		if r.reading && r.readSequence == sequence {
			r.timedOut = true
			_ = r.pipe.Close()
		}
	})
}
