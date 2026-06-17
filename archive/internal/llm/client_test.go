package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
)

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     llm.Config
		wantErr error
	}{
		{
			name: "vertex backend ok",
			cfg: llm.Config{
				ProjectID: "p", Location: "us-central1", Model: "gemini-2.0-flash",
			},
			wantErr: nil,
		},
		{
			name:    "missing project",
			cfg:     llm.Config{Location: "us-central1", Model: "gemini-2.0-flash"},
			wantErr: llm.ErrAuthMissing,
		},
		{
			name:    "missing location",
			cfg:     llm.Config{ProjectID: "p", Model: "gemini-2.0-flash"},
			wantErr: llm.ErrAuthMissing,
		},
		{
			name:    "missing model",
			cfg:     llm.Config{ProjectID: "p", Location: "us-central1"},
			wantErr: llm.ErrInvalidConfig,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("Validate: %v; want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate: got %v; want errors.Is(%v)", err, tc.wantErr)
			}
		})
	}
}

func TestLoadFromEnv_VertexConfig(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "incident-kb")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	t.Setenv("ORION_LLM_MODEL", "gemini-2.0-flash")
	cfg, err := llm.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "incident-kb" {
		t.Errorf("ProjectID=%q", cfg.ProjectID)
	}
	if cfg.Location != "us-central1" {
		t.Errorf("Location=%q", cfg.Location)
	}
	if cfg.Model == "" {
		t.Error("Model is empty")
	}
}

func TestLoadFromEnv_DefaultsModel(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "p")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	t.Setenv("ORION_LLM_MODEL", "")
	cfg, err := llm.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model == "" {
		t.Error("Model should default to a non-empty model name")
	}
}

func TestLoadFromEnv_MissingProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	_, err := llm.LoadFromEnv()
	if !errors.Is(err, llm.ErrAuthMissing) {
		t.Errorf("err=%v; want ErrAuthMissing", err)
	}
}

// fakeGenerator implements llm.Generator for tests of downstream consumers.
type fakeGenerator struct {
	resp llm.GenerateResponse
	err  error
	got  llm.GenerateRequest
}

func (f *fakeGenerator) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResponse, error) {
	f.got = req
	if f.err != nil {
		return llm.GenerateResponse{}, f.err
	}
	return f.resp, nil
}

func TestGenerator_InterfaceContract(t *testing.T) {
	g := &fakeGenerator{
		resp: llm.GenerateResponse{Text: "hello", Model: "gemini-2.0-flash"},
	}

	// Compile-time check: fakeGenerator implements Generator.
	var _ llm.Generator = g

	resp, err := g.Generate(context.Background(), llm.GenerateRequest{
		System:    "you are a JSON emitter",
		User:      "produce {}",
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" {
		t.Errorf("resp.Text=%q", resp.Text)
	}
	if g.got.User != "produce {}" {
		t.Errorf("captured user=%q", g.got.User)
	}
}
