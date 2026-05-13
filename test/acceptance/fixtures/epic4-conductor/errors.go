// Pinned target for rvl-cli `swallowed-error` (RC-021).
//
// CleanupHandler discards the inner error from os.Remove without
// wrapping or logging it. See README.md for the gap description.
package epic4conductor

import "os"

func CleanupHandler(path string) {
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}
}
