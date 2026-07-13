package formal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// STPA-UCA → formal-invariant synthesis (or-56c.2): from a ratified spec's
// design texts and the ratified STPA control structure + UCAs, an LLM DRAFTS a
// candidate formal model — and a HUMAN ratifies it, exactly the STPA
// questionnaire posture. The model is a PROOF-DOMAIN artifact: the untrusted
// generation fleet never authors its own proof, and the drafted candidate has
// no proof authority until a human signature anchors it by hash.

// designModelKind is the store key for the project's design-proof model.
const designModelKind = "formal_design_model"

// DefaultBackend is the recorded checker selection. FizzBee is the default;
// TLA+/Apalache is the documented escape hatch, selected by amending the
// artifact — never silently.
const DefaultBackend = "fizzbee"

// DesignModel is the ratifiable design-proof artifact.
type DesignModel struct {
	ModelText        string    `json:"model_text"`        // the .fizz source (draft or ratified)
	Hash             string    `json:"hash"`              // sha256 of ModelText — the ratification anchor
	Backend          string    `json:"backend"`           // recorded backend selection (fizzbee | apalache)
	BackendAvailable bool      `json:"backend_available"` // toolchain presence at synthesis time
	TriggerReason    string    `json:"trigger_reason"`    // why the design proof fired (auditable)
	Ratified         bool      `json:"ratified"`
	RatifiedBy       string    `json:"ratified_by,omitempty"`
	Created          time.Time `json:"created"`
}

// SynthesisInput is what the drafting model sees: ratified design surfaces only.
type SynthesisInput struct {
	Intent      string
	DesignTexts []string
	Structure   stpa.ControlStructure
	UCAs        []stpa.UCA
}

// Synthesizer drafts a candidate model from ratified design inputs. The
// conductor supplies the LLM adapter; nil means synthesis is unavailable.
type Synthesizer func(ctx context.Context, in SynthesisInput) (string, error)

// SynthesizeDesignModel runs the calibration trigger over the spec's shape
// and, when it fires, drafts + validates + persists an UNRATIFIED design
// model. A stateless spec produces none (nil, nil). An existing artifact for
// the project is never overwritten — re-planning must not un-ratify a model.
func SynthesizeDesignModel(ctx context.Context, store *contextstore.Store, projectID string, tier reliabilitytier.Tier, in SynthesisInput, synth Synthesizer) (*DesignModel, error) {
	if store == nil || synth == nil {
		return nil, nil
	}
	if existing, ok, err := LoadDesignModel(ctx, store, projectID); err == nil && ok {
		return &existing, nil
	}
	dec := ShouldCheck(TriggerInput{
		Tier:           tier,
		DesignTexts:    append([]string{in.Intent}, in.DesignTexts...),
		Controllers:    len(in.Structure.Controllers),
		ControlActions: len(in.Structure.Actions),
	})
	if !dec.Fire {
		return nil, nil // stateless shape: the design proof is calibrated off
	}
	draft, err := synth(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("design-model synthesis: %w", err)
	}
	draft = strings.TrimSpace(stripFence(draft))
	if err := validateDraft(draft); err != nil {
		return nil, fmt.Errorf("design-model draft rejected: %w", err)
	}
	sum := sha256.Sum256([]byte(draft))
	dm := DesignModel{
		ModelText:        draft,
		Hash:             hex.EncodeToString(sum[:]),
		Backend:          DefaultBackend,
		BackendAvailable: ResolveFizzBeeDir() != "",
		TriggerReason:    dec.Reason,
		Ratified:         false,
		Created:          time.Now().UTC(),
	}
	if err := saveDesignModel(ctx, store, projectID, dm); err != nil {
		return nil, err
	}
	return &dm, nil
}

// RatifyDesignModel is the human signature: it anchors the EXACT reviewed
// bytes by hash. A mismatched hash refuses — the artifact changed since the
// human read it, so the ratification would vouch for unseen content.
func RatifyDesignModel(ctx context.Context, store *contextstore.Store, projectID, hash, who string) (DesignModel, error) {
	dm, ok, err := LoadDesignModel(ctx, store, projectID)
	if err != nil {
		return DesignModel{}, err
	}
	if !ok {
		return DesignModel{}, fmt.Errorf("ratify design model: no draft exists for project %s", projectID)
	}
	if dm.Hash != hash {
		return DesignModel{}, fmt.Errorf("ratify design model: hash %.12s does not match the stored draft %.12s — review the current draft and ratify its exact hash", hash, dm.Hash)
	}
	if strings.TrimSpace(who) == "" {
		return DesignModel{}, fmt.Errorf("ratify design model: a ratifier identity is required")
	}
	dm.Ratified = true
	dm.RatifiedBy = strings.TrimSpace(who)
	if err := saveDesignModel(ctx, store, projectID, dm); err != nil {
		return DesignModel{}, err
	}
	return dm, nil
}

// LoadDesignModel retrieves the project's design model (draft or ratified).
func LoadDesignModel(ctx context.Context, store *contextstore.Store, projectID string) (DesignModel, bool, error) {
	var dm DesignModel
	var found bool
	err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, designModelKind)
		if err != nil || !ok {
			return err
		}
		found = true
		return json.Unmarshal([]byte(e.Payload), &dm)
	})
	if err != nil {
		return DesignModel{}, false, fmt.Errorf("design model load: %w", err)
	}
	return dm, found, nil
}

// WriteModelFile materializes a RATIFIED model for the checker; a draft has
// no proof authority and is refused.
func (dm DesignModel) WriteModelFile(dir string) (string, error) {
	if !dm.Ratified {
		return "", fmt.Errorf("design model %.12s is an unratified draft — it cannot enter the proof domain", dm.Hash)
	}
	p := filepath.Join(dir, "design_model.fizz")
	if err := os.WriteFile(p, []byte(dm.ModelText), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func saveDesignModel(ctx context.Context, store *contextstore.Store, projectID string, dm DesignModel) error {
	b, err := json.Marshal(dm)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, projectID, designModelKind, string(b), 0)
	})
}

// validateDraft compiles the draft's obligations in a scratch file: the same
// total-binding + zero-invariant rules a ratified model must satisfy
// (or-56c.3) apply to the CANDIDATE — a human should never be asked to ratify
// a model the gate would immediately refuse.
func validateDraft(draft string) error {
	if draft == "" {
		return fmt.Errorf("empty draft")
	}
	tmp, err := os.MkdirTemp("", "orion-design-draft-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	p := filepath.Join(tmp, "draft.fizz")
	if err := os.WriteFile(p, []byte(draft), 0o644); err != nil {
		return err
	}
	_, err = CompileObligations(p)
	return err
}

// stripFence removes a markdown code fence if the model wrapped its answer.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return s
}
