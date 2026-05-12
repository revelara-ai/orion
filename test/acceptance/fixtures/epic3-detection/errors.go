// Pinned target for rvl-cli `swallowed-error` (RC-021).
// See README.md for the gap description.
package epic3detection

import "fmt"

func LoadConfig(path string) *string {
	_, err := parseFile(path)
	if err != nil {
		return nil
	}
	out := "ok"
	return &out
}

func parseFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	return path, nil
}
