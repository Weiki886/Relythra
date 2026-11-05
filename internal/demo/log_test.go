package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogRotationIsBoundedAndKeepsCompleteLines(t *testing.T) {
	dir := t.TempDir()
	w, err := NewRotatingLog(filepath.Join(dir, "requests.log"), 128)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	line := strings.Repeat("a", 63) + "\n"
	for range 10 {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("files = %d", len(entries))
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > 128 || !strings.HasSuffix(string(b), "\n") {
			t.Fatalf("invalid rotated data %d", len(b))
		}
	}
	if _, err := w.Write([]byte(strings.Repeat("b", 129))); err == nil {
		t.Fatal("oversized record accepted")
	}
}
