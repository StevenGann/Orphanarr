package pipeline

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/layout"
	"github.com/StevenGann/Orphanarr/internal/media"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func filepathGlob(pattern string) ([]string, error) { return filepath.Glob(pattern) }

func movieLibrary(root string) layout.Library {
	return layout.Library{Type: media.Movie, Root: root}
}
