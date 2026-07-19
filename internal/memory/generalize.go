package memory

import (
	"context"
	"fmt"
	"strings"
)

// GeneralizeItem widens a project-scoped item to DEVELOPER scope (or-gb1.6):
// visible to every project — which is exactly why the transition MUST redact
// the origin project's literals (module paths, routes, identifiers) first.
// Redaction is not optional at this boundary: a developer-scoped LTM item
// carrying another project's literals is cross-project leakage and a poisoning
// vector in one. Every supplied literal is replaced (case-insensitive) with
// the [redacted] placeholder before the scope widens, atomically.
func (s *Store) GeneralizeItem(ctx context.Context, id string, literals []string) error {
	var content string
	err := s.db.QueryRowContext(ctx, `SELECT content FROM memory_items WHERE id=?`, id).Scan(&content)
	if err != nil {
		return fmt.Errorf("memory generalize: item %s: %w", id, err)
	}
	red := RedactLiterals(content, literals)
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_items SET content=?, project_id='' WHERE id=?`, red, id); err != nil {
		return fmt.Errorf("memory generalize: %w", err)
	}
	return nil
}

// RedactLiterals replaces every occurrence of each literal (case-insensitive)
// with "[redacted]". Empty/whitespace literals are ignored. Exported for the
// other developer-scope boundary: self-evolution skill promotion (skills are
// data-dir global).
func RedactLiterals(content string, literals []string) string {
	for _, lit := range literals {
		lit = strings.TrimSpace(lit)
		if lit == "" {
			continue
		}
		lower := strings.ToLower(content)
		needle := strings.ToLower(lit)
		var b strings.Builder
		for {
			i := strings.Index(lower, needle)
			if i < 0 {
				b.WriteString(content)
				break
			}
			b.WriteString(content[:i])
			b.WriteString("[redacted]")
			content = content[i+len(lit):]
			lower = lower[i+len(needle):]
		}
		content = b.String()
	}
	return content
}
