package app

import "sync"

type stateWrite struct {
	version uint64
	data    []byte
}

// StateWriter coalesces local saves independently of IPC. Version ordering also
// prevents an older asynchronous save from overwriting the final close flush.
type StateWriter struct {
	path       string
	mu         sync.Mutex
	ioMu       sync.Mutex
	latest     stateWrite
	written    uint64
	lastData   []byte
	lastErr    error
	invalidate func()
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once
}

func NewStateWriter(path string) *StateWriter {
	w := &StateWriter{path: path, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go w.run()
	return w
}
func (w *StateWriter) SetInvalidate(fn func()) { w.mu.Lock(); w.invalidate = fn; w.mu.Unlock() }
func (w *StateWriter) Queue(data []byte) {
	w.mu.Lock()
	w.latest = stateWrite{version: w.latest.version + 1, data: append([]byte(nil), data...)}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *StateWriter) run() {
	defer close(w.done)
	for {
		select {
		case <-w.stop:
			return
		case <-w.wake:
			w.FlushLatest()
		}
	}
}
func (w *StateWriter) Flush(data []byte) error { w.Queue(data); return w.FlushLatest() }
func (w *StateWriter) FlushLatest() error {
	w.ioMu.Lock()
	defer w.ioMu.Unlock()
	w.mu.Lock()
	request := w.latest
	if request.version <= w.written {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	err := WriteState(w.path, request.data)
	w.mu.Lock()
	if err == nil {
		w.written = request.version
		w.lastData = request.data
	}
	w.lastErr = err
	invalidate := w.invalidate
	w.mu.Unlock()
	if invalidate != nil {
		invalidate()
	}
	return err
}
func (w *StateWriter) Result() ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastData, w.lastErr
}
func (w *StateWriter) Close() { w.once.Do(func() { w.FlushLatest(); close(w.stop); <-w.done }) }
