package rotatinglog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterRotatesWithinBoundedBackupCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "iot-edge.log")
	w, err := Open(path, 1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte(strings.Repeat("x", 700))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("missing %s", name)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal("unbounded backup exists")
	}
}
