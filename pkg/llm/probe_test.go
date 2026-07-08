package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeProvider scripts Chat/Models responses for probe tests.
type fakeProvider struct {
	chat    *ChatResponse
	chatErr error
	models  []ModelInfo
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return f.chat, f.chatErr
}
func (f *fakeProvider) ChatStream(ctx context.Context, req ChatRequest, _ func(string)) (*ChatResponse, error) {
	return f.Chat(ctx, req)
}
func (f *fakeProvider) Models(context.Context) ([]ModelInfo, error) { return f.models, nil }
func (f *fakeProvider) Ping(context.Context) error                  { return nil }

func TestProbeToolCapable(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopToolUse,
		Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{
			ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":"ping"}`),
		}}},
	}}
	ok, err := Probe(context.Background(), f)
	if err != nil || !ok {
		t.Fatalf("Probe = (%v,%v), want (true,nil)", ok, err)
	}
}

func TestProbeProseOnlyModel(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopEndTurn,
		Content:    []ContentBlock{{Type: BlockText, Text: "I would call echo with ping"}},
	}}
	ok, err := Probe(context.Background(), f)
	if err != nil || ok {
		t.Fatalf("Probe = (%v,%v), want (false,nil)", ok, err)
	}
}

func TestProbeWrongToolOrBadJSON(t *testing.T) {
	f := &fakeProvider{chat: &ChatResponse{
		StopReason: StopToolUse,
		Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{
			ID: "c1", Name: "echo", Input: json.RawMessage(`{"text":`), // malformed
		}}},
	}}
	if ok, _ := Probe(context.Background(), f); ok {
		t.Error("malformed tool input must not count as tool-capable")
	}
}

func TestProbeTransportError(t *testing.T) {
	f := &fakeProvider{chatErr: errors.New("connection refused")}
	if _, err := Probe(context.Background(), f); err == nil {
		t.Error("transport errors must surface, not read as incapable")
	}
}

func TestAdvertisesTools(t *testing.T) {
	f := &fakeProvider{models: []ModelInfo{
		{ID: "claude-opus-4-8", Tools: true},
		{ID: "qwen3-32b", Tools: false},
	}}
	if !AdvertisesTools(context.Background(), f, "claude-opus-4-8") {
		t.Error("advertised model must return true")
	}
	if AdvertisesTools(context.Background(), f, "qwen3-32b") {
		t.Error("Tools:false listing must return false")
	}
	if AdvertisesTools(context.Background(), f, "unlisted") {
		t.Error("unlisted model must return false (probe decides)")
	}
}
