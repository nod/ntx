// Package themes finds site themes on disk. A theme is a folder under
// themes_path holding templates/ (and usually static/). Sites refer to a
// theme by name only; the browser never supplies a path.
package themes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nod/ntx/internal/validate"
)

// List returns the names of valid themes under root, sorted: folders whose
// name passes validate.Key and that contain a templates/ folder.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading themes folder: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || validate.Key(e.Name()) != nil {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, e.Name(), "templates")); err == nil && fi.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Dir resolves a theme name to its folder. The name must pass validate.Key,
// so it is a single path segment and can't escape root, and the folder
// must exist with a templates/ folder inside.
func Dir(root, name string) (string, error) {
	if err := validate.Key(name); err != nil {
		return "", fmt.Errorf("theme %w", err)
	}
	dir := filepath.Join(root, name)
	if fi, err := os.Stat(filepath.Join(dir, "templates")); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("theme %q not found in %s", name, root)
	}
	return dir, nil
}
