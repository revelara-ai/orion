// Pinned target for rvl-cli `missing-retry` (RC-019).
//
// FetchUpstream calls http.Get without a retry/backoff library mention
// in the negation window. See README.md for the gap description.
package epic4conductor

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
