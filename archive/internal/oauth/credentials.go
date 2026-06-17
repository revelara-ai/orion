// Forked from github.com/revelara-ai/polaris/internal/connector/credentials/manager.go
// at SHA 78d5166b on 2026-05-11. Pending consolidation per orion-13j.
//
// Direct fork; the cryptographic shape is identical (AES-256-GCM,
// base64-encoded ciphertext with nonce prefix). Field naming aligned
// with Orion conventions.

package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrInvalidKey signals the encryption key has wrong length.
var ErrInvalidKey = errors.New("oauth: encryption key must be exactly 32 bytes (AES-256)")

// Manager encrypts and decrypts OAuth credentials at rest. The
// encryption key is an AES-256 key (32 bytes) loaded from
// configuration; rotation is operator-driven (re-encrypt all rows
// with the new key, then retire the old one).
type Manager struct {
	encryptionKey []byte
}

// NewManager validates the key length and returns a Manager.
func NewManager(encryptionKey []byte) (*Manager, error) {
	if len(encryptionKey) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKey, len(encryptionKey))
	}
	// Copy so the caller can zero its slice without affecting us.
	key := make([]byte, 32)
	copy(key, encryptionKey)
	return &Manager{encryptionKey: key}, nil
}

// Encrypt serializes credentials as JSON and encrypts with
// AES-256-GCM. The output is base64(nonce || ciphertext || tag).
func (m *Manager) Encrypt(credentials map[string]any) (string, error) {
	plaintext, err := json.Marshal(credentials)
	if err != nil {
		return "", fmt.Errorf("oauth: marshal credentials: %w", err)
	}
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("oauth: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("oauth: create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("oauth: generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt is the inverse: base64-decode, split nonce + ciphertext,
// AES-GCM open, JSON unmarshal.
func (m *Manager) Decrypt(encryptedData string) (map[string]any, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, fmt.Errorf("oauth: decode base64: %w", err)
	}
	block, err := aes.NewCipher(m.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("oauth: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("oauth: create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("oauth: ciphertext too short")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: decrypt: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plaintext, &out); err != nil {
		return nil, fmt.Errorf("oauth: unmarshal credentials: %w", err)
	}
	return out, nil
}
