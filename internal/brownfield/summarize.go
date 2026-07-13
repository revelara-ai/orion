package brownfield

import "strings"

// TestSummary is a per-package digest of a `go test ./...` run (or-rbc): the
// green/failed/no-tests tallies for a "N green / M failing" headline, plus the
// output block of each FAILING package so a report can expand failures ALONE
// instead of dumping the whole (minutes-long) run.
type TestSummary struct {
	Green   []string          // packages that passed ("ok")
	Failed  []string          // packages that failed ("FAIL")
	NoTests []string          // packages with no test files ("?")
	Blocks  map[string]string // failing package -> its output block (details + FAIL line)
}

// Summarize parses combined `go test ./...` output into a TestSummary. go test
// prints each package's output contiguously, ending with an "ok/FAIL/? <pkg>"
// line, so the lines accumulated since the previous verdict are that package's
// block — kept only for failures (a green package has nothing worth expanding).
// The bare terminal "FAIL" line (no package token) is ignored.
func Summarize(output string) TestSummary {
	sum := TestSummary{Blocks: map[string]string{}}
	var block []string
	for _, line := range strings.Split(output, "\n") {
		pkg, verdict, ok := packageVerdict(line)
		if !ok {
			block = append(block, line)
			continue
		}
		switch verdict {
		case "ok":
			sum.Green = append(sum.Green, pkg)
		case "?":
			sum.NoTests = append(sum.NoTests, pkg)
		case "FAIL":
			sum.Failed = append(sum.Failed, pkg)
			block = append(block, line) // the FAIL line belongs to this package's block
			sum.Blocks[pkg] = strings.Trim(strings.Join(block, "\n"), "\n")
		}
		block = nil
	}
	return sum
}
