package github

import (
	"os"
	"path/filepath"
)

// writeFileMkdir creates parent dirs as needed and writes content with
// permission 0644. The file is overwritten if it exists.
func writeFileMkdir(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}
