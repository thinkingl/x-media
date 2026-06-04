package media

import (
	"path/filepath"
	"runtime"
	"testing"
)

func testFixturePath(t *testing.T, relPath string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get caller info")
	}
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(filename), relPath))
	if err != nil {
		t.Fatalf("failed to resolve path: %v", err)
	}
	return abs
}
