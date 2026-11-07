package themes

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, parts ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(parts...), 0755); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdir(t, root, "good", "templates")
	mkdir(t, root, "Bad Name", "templates")
	mkdir(t, root, "notheme")
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestListOnlyValidThemes(t *testing.T) {
	got, err := List(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "good" {
		t.Errorf("List = %v, want [good]", got)
	}
}

func TestDirRejectsTraversal(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"../etc", "a/b", "missing", "notheme", "Bad Name", ""} {
		if _, err := Dir(root, name); err == nil {
			t.Errorf("Dir(%q): expected error", name)
		}
	}
	got, err := Dir(root, "good")
	if err != nil || got != filepath.Join(root, "good") {
		t.Errorf("Dir(good) = %q, %v", got, err)
	}
}

func TestShippedThemesValid(t *testing.T) {
	got, err := List("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"default": false, "blog": false, "nerdtx": false}
	for _, n := range got {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("shipped theme %q missing from List: %v", n, got)
		}
	}
}
