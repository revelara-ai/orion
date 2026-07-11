package reliabilityfloor

import (
	"fmt"
	"strings"
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// RenderContext renders signals as an advisory prompt block. Pure; "" when empty.
func RenderContext(sigs []Signal) string {
	if len(sigs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Reliability floor (org-grounded; advisory)\n")
	b.WriteString("Honor these where they apply to your change:\n")
	for _, s := range sigs {
		fmt.Fprintf(&b, "- [%s] %s (%s)", s.Severity, s.Title, s.ID)
		if strings.TrimSpace(s.Why) != "" {
			fmt.Fprintf(&b, " — %s", strings.TrimSpace(s.Why))
		}
		b.WriteString("\n")
	}
	return b.String()
}
