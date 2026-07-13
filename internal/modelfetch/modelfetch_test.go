package modelfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeAssets(t *testing.T) ([]Asset, *httptest.Server, *int) {
	t.Helper()
	hits := 0
	content := map[string]string{
		"model.onnx":     "fake onnx bytes",
		"tokenizer.json": `{"fake":"tokenizer"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		name := filepath.Base(r.URL.Path)
		body, ok := content[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	var assets []Asset
	for name, body := range content {
		sum := sha256.Sum256([]byte(body))
		assets = append(assets, Asset{
			Name: name, URL: srv.URL + "/" + name,
			SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)),
		})
	}
	return assets, srv, &hits
}

// The or-c6zf.5 done-when trio: fetch provisions both files; a re-run is a
// no-op (checksums match, zero downloads); corruption re-downloads.
func TestFetchIdempotentAndSelfHealing(t *testing.T) {
	assets, _, hits := fakeAssets(t)
	dir := t.TempDir()
	ctx := context.Background()

	res, err := Fetch(ctx, dir, assets, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets {
		if _, err := os.Stat(filepath.Join(dir, a.Name)); err != nil {
			t.Fatalf("%s not provisioned: %v", a.Name, err)
		}
	}
	if *hits != 2 || len(res) != 2 {
		t.Fatalf("first run must download both: hits=%d res=%d", *hits, len(res))
	}

	// Re-run: checksums match → skipped, ZERO new downloads.
	res, err = Fetch(ctx, dir, assets, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if !r.Skipped {
			t.Fatalf("re-run must skip verified assets: %+v", res)
		}
	}
	if *hits != 2 {
		t.Fatalf("re-run must not touch the network: hits=%d", *hits)
	}

	// Corrupt one file → checksum mismatch detected → exactly one re-download.
	if err := os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(ctx, dir, assets, nil); err != nil {
		t.Fatal(err)
	}
	if *hits != 3 {
		t.Fatalf("corruption must trigger exactly one re-download: hits=%d", *hits)
	}
	if ok, detail := Verify(dir, assets); !ok {
		t.Fatalf("post-heal verify failed: %s", detail)
	}
}

// A hostile/changed upstream never installs: the mismatched download is
// rejected and nothing replaces the target.
func TestFetchRefusesChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "NOT the pinned bytes")
	}))
	defer srv.Close()
	assets := []Asset{{Name: "model.onnx", URL: srv.URL + "/model.onnx", SHA256: strings.Repeat("ab", 32), Size: 5}}
	dir := t.TempDir()
	if _, err := Fetch(context.Background(), dir, assets, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("mismatch must refuse to install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model.onnx")); !os.IsNotExist(err) {
		t.Fatal("a rejected download must leave no installed file")
	}
	if _, err := os.Stat(filepath.Join(dir, "model.onnx.tmp")); !os.IsNotExist(err) {
		t.Fatal("a rejected download must clean its temp file")
	}
}
