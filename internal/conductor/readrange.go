package conductor

import (
	"fmt"
	"strings"
)

// sliceLines applies an optional 1-based line range to a file's content
// (or-mvr.14 lever 3: full-file reads dominated both context and iteration
// spend — ranged reads let the model pay for only what it needs). startLine<=0
// means "from the top"; lineCount<=0 means "to the end". Out-of-range starts
// return an explicit marker rather than silent emptiness.
func sliceLines(content string, startLine, lineCount int) string {
	if startLine <= 0 && lineCount <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	start := startLine
	if start <= 0 {
		start = 1
	}
	if start > len(lines) {
		return fmt.Sprintf("[range starts at line %d but the file has only %d lines]", start, len(lines))
	}
	end := len(lines)
	if lineCount > 0 && start-1+lineCount < end {
		end = start - 1 + lineCount
	}
	out := strings.Join(lines[start-1:end], "\n")
	return fmt.Sprintf("[lines %d-%d of %d]\n%s", start, end, len(lines), out)
}
