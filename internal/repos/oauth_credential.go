package repos

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/oauth"
)

// OAuthCredentialRepo persists encrypted OAuth credentials. Every
// write goes through *database.RLSPool so RLS enforces tenant
// isolation. Plaintext tokens never touch this table — the Manager
// encrypts before insert and decrypts on read.
type OAuthCredentialRepo struct {
	pool *database.RLSPool
	mgr  *oauth.Manager
}

// NewOAuthCredentialRepo wraps an RLSPool with an oauth.Manager for
// encrypt/decrypt.
func NewOAuthCredentialRepo(p *database.RLSPool, mgr *oauth.Manager) *OAuthCredentialRepo {
	return &OAuthCredentialRepo{pool: p, mgr: mgr}
}

// EncryptedOAuthCredential is the durable shape (encrypted blob +
// metadata). Callers normally interact with DecryptedCredentials
// via Get / Upsert; this struct is exposed for admin tooling.
type EncryptedOAuthCredential struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Provider      string
	EncryptedBlob string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DecryptedCredentials is the plaintext shape callers actually use.
// Mirrors oauth.OAuthTokens fields plus the row's id (so callers
// can update by id without a separate lookup). Held only in memory;
// the durable store keeps these encrypted.
type DecryptedCredentials struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Provider     string
	AccessToken  string //#nosec G117 -- in-memory bearer; durable store is encrypted_oauth_credential.encrypted_blob
	RefreshToken string //#nosec G117 -- in-memory bearer; durable store is encrypted_oauth_credential.encrypted_blob
	ExpiresAt    *time.Time
	Scope        string
	Extra        map[string]any
}

// Create encrypts + inserts a new row. Returns the assigned id +
// timestamps.
func (r *OAuthCredentialRepo) Create(ctx context.Context, provider string, tokens oauth.OAuthTokens) (*EncryptedOAuthCredential, error) {
	if provider == "" {
		return nil, fmt.Errorf("repos: provider required")
	}
	blob, err := r.mgr.Encrypt(tokensToMap(tokens))
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO encrypted_oauth_credential (org_id, provider, encrypted_blob)
		VALUES (current_setting('app.current_organization_id')::uuid, $1, $2)
		RETURNING id, org_id, provider, encrypted_blob, created_at, updated_at
	`
	row := r.pool.QueryRow(ctx, q, provider, blob)
	return scanEncryptedOAuth(row)
}

// Get fetches + decrypts by id.
func (r *OAuthCredentialRepo) Get(ctx context.Context, id uuid.UUID) (*DecryptedCredentials, error) {
	const q = `
		SELECT id, org_id, provider, encrypted_blob, created_at, updated_at
		FROM encrypted_oauth_credential
		WHERE id = $1
	`
	enc, err := scanEncryptedOAuth(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repos: get oauth_credential: %w", err)
	}
	return r.decrypt(enc)
}

// UpdateTokens re-encrypts the new tokens and stores them. Called by
// the registry's WireRefreshCallback when an adapter rotates.
func (r *OAuthCredentialRepo) UpdateTokens(ctx context.Context, id uuid.UUID, tokens oauth.OAuthTokens) error {
	blob, err := r.mgr.Encrypt(tokensToMap(tokens))
	if err != nil {
		return err
	}
	const q = `UPDATE encrypted_oauth_credential SET encrypted_blob = $2, updated_at = now() WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id, blob)
	if err != nil {
		return fmt.Errorf("repos: update oauth_credential: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a credential.
func (r *OAuthCredentialRepo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM encrypted_oauth_credential WHERE id = $1`
	res, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("repos: delete oauth_credential: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// decrypt converts an encrypted row into the plaintext shape.
func (r *OAuthCredentialRepo) decrypt(enc *EncryptedOAuthCredential) (*DecryptedCredentials, error) {
	m, err := r.mgr.Decrypt(enc.EncryptedBlob)
	if err != nil {
		return nil, err
	}
	out := &DecryptedCredentials{
		ID:       enc.ID,
		OrgID:    enc.OrgID,
		Provider: enc.Provider,
		Extra:    map[string]any{},
	}
	for k, v := range m {
		switch k {
		case "access_token":
			if s, ok := v.(string); ok {
				out.AccessToken = s
			}
		case "refresh_token":
			if s, ok := v.(string); ok {
				out.RefreshToken = s
			}
		case "expires_at":
			if s, ok := v.(string); ok {
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					out.ExpiresAt = &t
				}
			}
		case "scope":
			if s, ok := v.(string); ok {
				out.Scope = s
			}
		default:
			out.Extra[k] = v
		}
	}
	return out, nil
}

func tokensToMap(t oauth.OAuthTokens) map[string]any {
	m := map[string]any{
		"access_token": t.AccessToken,
	}
	if t.RefreshToken != "" {
		m["refresh_token"] = t.RefreshToken
	}
	if t.ExpiresAt != nil {
		m["expires_at"] = t.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if t.Scope != "" {
		m["scope"] = t.Scope
	}
	for k, v := range t.Extra {
		m[k] = v
	}
	return m
}

func scanEncryptedOAuth(row pgx.Row) (*EncryptedOAuthCredential, error) {
	var e EncryptedOAuthCredential
	if err := row.Scan(&e.ID, &e.OrgID, &e.Provider, &e.EncryptedBlob, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// avoid unused-import noise.
var _ = errors.New
