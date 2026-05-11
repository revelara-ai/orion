package oauth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func TestNewManagerRejectsWrongKeyLength(t *testing.T) {
	for _, size := range []int{0, 16, 24, 31, 33, 64} {
		_, err := NewManager(make([]byte, size))
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("size=%d: err = %v, want ErrInvalidKey", size, err)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(key)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	in := map[string]any{
		"access_token":  "ghs_abc",
		"refresh_token": "rt_xyz",
		"scope":         "read write",
		"extra_field":   "passthrough",
	}
	enc, err := mgr.Encrypt(in)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc == "" {
		t.Error("empty ciphertext")
	}
	out, err := mgr.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("key %s: got %v, want %v", k, out[k], v)
		}
	}
}

func TestEncryptionKeyCopyIsolation(t *testing.T) {
	// Mutating the caller's key after NewManager MUST NOT affect
	// the manager — it stores a defensive copy.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	mgr, _ := NewManager(key)
	enc, _ := mgr.Encrypt(map[string]any{"x": "y"})

	// Mutate caller's key.
	for i := range key {
		key[i] = 0xff
	}
	out, err := mgr.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt after key mutation: %v", err)
	}
	if out["x"] != "y" {
		t.Errorf("decrypt result corrupted: %v", out)
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mgr, _ := NewManager(key)
	enc, _ := mgr.Encrypt(map[string]any{"x": "y"})
	// Flip a byte in the middle. Base64-decode first to mutate raw
	// ciphertext, then re-encode.
	tampered := []byte(enc)
	tampered[len(tampered)/2] ^= 0x01
	if _, err := mgr.Decrypt(string(tampered)); err == nil {
		t.Error("expected decrypt to fail on tampered ciphertext")
	}
}

func TestDecryptRejectsTooShort(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mgr, _ := NewManager(key)
	if _, err := mgr.Decrypt("c2hvcnQ="); err == nil { // base64 "short"
		t.Error("expected decrypt to fail on too-short ciphertext")
	}
}

func TestDifferentKeysProduceDifferentCiphertext(t *testing.T) {
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	_, _ = rand.Read(keyA)
	_, _ = rand.Read(keyB)
	mgrA, _ := NewManager(keyA)
	mgrB, _ := NewManager(keyB)
	in := map[string]any{"x": "y"}
	encA, _ := mgrA.Encrypt(in)
	encB, _ := mgrB.Encrypt(in)
	if encA == encB {
		t.Error("expected different ciphertexts for different keys (nonce alone should still differ; but identity match is suspicious)")
	}
	if _, err := mgrA.Decrypt(encB); err == nil {
		t.Error("expected cross-key decrypt to fail")
	}
}

// stderrBuf is a small buffer that implements stderrWriter so the
// registry test can capture the rotation-persist-failed log without
// touching os.Stderr.
type stderrBuf struct{ bytes.Buffer }

func (b *stderrBuf) Write(p []byte) (int, error) { return b.Buffer.Write(p) }
