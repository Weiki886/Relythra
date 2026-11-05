package demo

import (
	"errors"
	"os"
	"sync"
)

// RotatingLog retains at most two bounded files; records are never split.
type RotatingLog struct {
	mu          sync.Mutex
	path        string
	limit, size int64
	file        *os.File
}

func NewRotatingLog(path string, limit int64) (*RotatingLog, error) {
	if limit <= 0 {
		return nil, errors.New("positive log limit required")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &RotatingLog{path: path, limit: limit, size: info.Size(), file: f}, nil
}
func (w *RotatingLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(len(p)) > w.limit {
		return 0, errors.New("log record too large")
	}
	if w.size+int64(len(p)) > w.limit {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *RotatingLog) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }
