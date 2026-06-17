// Pinned target for rvl-cli `missing-timeout` (RC-018).
//
// Comment intentionally avoids the regex-matched keyword so the
// matcher's negate window does not silently suppress the finding.
// See README.md for the gap description.
package epic4conductor

import "net/http"

func NewClient() *http.Client {
	return &http.Client{
		Transport: http.DefaultTransport,
	}
}
