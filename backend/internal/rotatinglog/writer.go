package rotatinglog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func Open(path string, maxBytes int64, backups int) (*Writer, error) {
	if !filepath.IsAbs(path) || maxBytes < 1024 || backups < 1 || backups > 100 {
		return nil, errors.New("invalid rotating log configuration")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	w := &Writer{path: filepath.Clean(path), maxBytes: maxBytes, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *Writer) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(value)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(value)
	w.size += int64(n)
	return n, err
}
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
func (w *Writer) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}
func (w *Writer) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	_ = os.Remove(w.path + "." + itoa(w.backups))
	for i := w.backups - 1; i >= 1; i-- {
		old := w.path + "." + itoa(i)
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, w.path+"."+itoa(i+1)); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	w.size = 0
	return w.open()
}
func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	buf := make([]byte, 0, 4)
	for value > 0 {
		buf = append([]byte{digits[value%10]}, buf...)
		value /= 10
	}
	return string(buf)
}

var _ io.WriteCloser = (*Writer)(nil)
