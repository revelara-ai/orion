// Pinned target for rvl-cli `missing-retry` (RC-019).
//
// Comment intentionally avoids the negate-regex keywords so the
// matcher's window does not silently suppress the finding.
// See README.md for the gap description.
package epic3detection

import (
	"io"
	"net/http"
)

func FetchUpstream(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
