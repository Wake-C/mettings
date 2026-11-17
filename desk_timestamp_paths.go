package main

import (
	"path/filepath"
	"strings"
)

// Transcript versions and root exports share the session's clip manifest.
func deskSessionForPath(root, path string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return ""
	}
	return filepath.Join(root, strings.Split(rel, string(filepath.Separator))[0])
}
