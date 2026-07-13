// Package modelfetch provisions the opt-in semantic-memory embedder's model
// assets (or-c6zf.5): the bge-base-en-v1.5 ONNX export + tokenizer from
// huggingface.co, pinned by SHA-256 — idempotent (a matching checksum skips
// the download; a corrupted file re-downloads), atomic (temp + rename).
//
// Licensing: bge-base-en-v1.5 is published by BAAI under the MIT license
// (https://huggingface.co/BAAI/bge-base-en-v1.5). Orion downloads it at
// provisioning time and never redistributes it.
package modelfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Asset is one pinned downloadable file.
type Asset struct {
	Name   string // filename under the target dir
	URL    string
	SHA256 string
	Size   int64 // informational (progress hint)
}

// BGEBaseAssets is the pinned bge-base-en-v1.5 set the GoMLX embedder loads.
// Checksums pinned 2026-07-13 from huggingface.co (model.onnx via its LFS
// ETag; tokenizer.json hashed from the fetched blob).
func BGEBaseAssets() []Asset {
	return []Asset{
		{
			Name:   "model.onnx",
			URL:    "https://huggingface.co/BAAI/bge-base-en-v1.5/resolve/main/onnx/model.onnx",
			SHA256: "9bc579acdba21c253c62a9bf866891355a63ffa3442b52c8a37d75b2ccb91848",
			Size:   435811539,
		},
		{
			Name:   "tokenizer.json",
			URL:    "https://huggingface.co/BAAI/bge-base-en-v1.5/resolve/main/tokenizer.json",
			SHA256: "d241a60d5e8f04cc1b2b3e9ef7a4921b27bf526d9f6050ab90f9267a1f9e5c66",
			Size:   711396,
		},
	}
}

// Result reports one asset's outcome.
type Result struct {
	Name    string
	Skipped bool // checksum already matched — no download
}

// Fetch provisions the assets into dir. Existing files with matching
// checksums are skipped; mismatches (corruption, partial downloads) are
// re-downloaded. Every write is temp+rename so a crash never leaves a
// half-written model behind a matching name.
func Fetch(ctx context.Context, dir string, assets []Asset, progress func(string)) ([]Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("model dir: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	var out []Result
	for _, a := range assets {
		dst := filepath.Join(dir, a.Name)
		if sum, err := fileSHA256(dst); err == nil && sum == a.SHA256 {
			progress(a.Name + ": present (checksum ok) — skipped")
			out = append(out, Result{Name: a.Name, Skipped: true})
			continue
		}
		progress(fmt.Sprintf("%s: downloading (%d MB)…", a.Name, a.Size/(1<<20)))
		if err := download(ctx, client, a, dst); err != nil {
			return out, fmt.Errorf("%s: %w", a.Name, err)
		}
		progress(a.Name + ": downloaded + verified")
		out = append(out, Result{Name: a.Name})
	}
	return out, nil
}

func download(ctx context.Context, client *http.Client, a Asset, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d from %s", resp.StatusCode, a.URL)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp) // #nosec G304 -- operator-chosen model dir
	if err != nil {
		return err
	}
	h := sha256.New()
	_, cerr := io.Copy(io.MultiWriter(f, h), resp.Body)
	if err := f.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
		_ = os.Remove(tmp)
		return fmt.Errorf("checksum mismatch: got %s want %s (upstream changed or download corrupted — refusing to install)", got, a.SHA256)
	}
	return os.Rename(tmp, dst)
}

// Verify reports whether dir holds every asset with a matching checksum.
func Verify(dir string, assets []Asset) (ok bool, detail string) {
	for _, a := range assets {
		sum, err := fileSHA256(filepath.Join(dir, a.Name))
		if err != nil {
			return false, a.Name + " missing"
		}
		if sum != a.SHA256 {
			return false, a.Name + " checksum mismatch"
		}
	}
	return true, ""
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-chosen model dir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyQuick is the cheap doctor-grade probe: presence + exact size (a full
// checksum of a ~400MB model on every doctor run would be needless I/O; the
// fetch path always verifies the real SHA-256).
func VerifyQuick(dir string, assets []Asset) (ok bool, detail string) {
	for _, a := range assets {
		st, err := os.Stat(filepath.Join(dir, a.Name))
		if err != nil {
			return false, a.Name + " missing"
		}
		if a.Size > 0 && st.Size() != a.Size {
			return false, fmt.Sprintf("%s size %d != expected %d", a.Name, st.Size(), a.Size)
		}
	}
	return true, ""
}
