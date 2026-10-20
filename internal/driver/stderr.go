package driver

import (
	"bytes"
	"strings"
	"sync"
)

// maxStderrBytes bounds what Betty relays from one driver process's stderr.
const maxStderrBytes = 64 << 10

// lineLog splits a driver's stderr into lines for OnDriverLog. It relays at
// most maxStderrBytes, then reports once that the rest was dropped.
type lineLog struct {
	emit func(line string)

	mu        sync.Mutex
	partial   []byte
	remaining int
	truncated bool
}

func newLineLog(emit func(string)) *lineLog {
	return &lineLog{emit: emit, remaining: maxStderrBytes}
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if l.truncated {
		return n, nil
	}
	if len(p) > l.remaining {
		p = p[:l.remaining]
		l.truncated = true
	}
	l.remaining -= len(p)
	l.partial = append(l.partial, p...)
	for {
		i := bytes.IndexByte(l.partial, '\n')
		if i < 0 {
			break
		}
		l.line(l.partial[:i])
		l.partial = l.partial[i+1:]
	}
	if l.truncated {
		l.flushLocked()
		l.emit("(further stderr output dropped)")
	}
	return n, nil
}

// flush relays a final line that had no newline.
func (l *lineLog) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
}

func (l *lineLog) flushLocked() {
	if len(l.partial) > 0 {
		l.line(l.partial)
		l.partial = nil
	}
}

func (l *lineLog) line(raw []byte) {
	if line := strings.TrimRight(string(raw), "\r"); line != "" {
		l.emit(line)
	}
}
