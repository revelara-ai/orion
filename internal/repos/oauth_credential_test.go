package repos

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/oauth"
)

func newOAuthRepo(t *testing.T) (*database.RLSPool, *OAuthCredentialRepo) {
	t.Helper()
	rls := newRLSPool(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	mgr, err := oauth.NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	return rls, NewOAuthCredentialRepo(rls, mgr)
}

func TestOAuthCredential_CreateGet(t *testing.T) {
	_, repo := newOAuthRepo(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)

	expiry := time.Now().Add(1 * time.Hour).UTC()
	created, err := repo.Create(ctx, "linear", oauth.OAuthTokens{
		AccessToken:  "tok-a",
		RefreshToken: "tok-r",
		ExpiresAt:    &expiry,
		Scope:        "issues:create",
		Extra:        map[string]any{"workspace_id": "ws-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("zero UUID")
	}
	if created.OrgID != orgID {
		t.Errorf("OrgID = %s", created.OrgID)
	}
	if created.Provider != "linear" {
		t.Errorf("Provider = %s", created.Provider)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "tok-a" {
		t.Errorf("AccessToken = %s", got.AccessToken)
	}
	if got.RefreshToken != "tok-r" {
		t.Errorf("RefreshToken = %s", got.RefreshToken)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiry.Truncate(time.Second)) {
		t.Errorf("ExpiresAt = %v, want ~ %v", got.ExpiresAt, expiry)
	}
	if got.Scope != "issues:create" {
		t.Errorf("Scope = %s", got.Scope)
	}
	if got.Extra["workspace_id"] != "ws-1" {
		t.Errorf("Extra = %v", got.Extra)
	}
}

func TestOAuthCredential_UpdateTokensReencrypts(t *testing.T) {
	_, repo := newOAuthRepo(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)

	created, _ := repo.Create(ctx, "linear", oauth.OAuthTokens{
		AccessToken:  "v1-access",
		RefreshToken: "v1-refresh",
	})
	if err := repo.UpdateTokens(ctx, created.ID, oauth.OAuthTokens{
		AccessToken:  "v2-access",
		RefreshToken: "v2-refresh",
	}); err != nil {
		t.Fatalf("UpdateTokens: %v", err)
	}
	got, _ := repo.Get(ctx, created.ID)
	if got.AccessToken != "v2-access" || got.RefreshToken != "v2-refresh" {
		t.Errorf("after rotation: %+v", got)
	}
}

func TestOAuthCredential_RLSIsolation(t *testing.T) {
	_, repo := newOAuthRepo(t)
	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), "ua", orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), "ub", orgB, nil)

	created, err := repo.Create(ctxA, "linear", oauth.OAuthTokens{AccessToken: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Get(ctxB, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("orgB Get of orgA's cred: %v, want ErrNotFound", err)
	}
}

func TestOAuthCredential_DeleteNotFound(t *testing.T) {
	_, repo := newOAuthRepo(t)
	ctx := database.WithRLSContext(context.Background(), "u", uuid.New(), nil)
	err := repo.Delete(ctx, uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete nonexistent: %v, want ErrNotFound", err)
	}
}
