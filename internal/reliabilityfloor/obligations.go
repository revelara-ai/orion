package reliabilityfloor

import "sort"

// Split partitions signals into mechanizable (a golangci-lint check) and advisory.
func Split(sigs []Signal) (mechanizable, advisory []Signal) {
	for _, s := range sigs {
		if s.Check.Kind == CheckGolangciLint && len(s.Check.Linters) > 0 {
			mechanizable = append(mechanizable, s)
		} else {
			advisory = append(advisory, s)
		}
	}
	return
}

// LintArgs builds the golangci-lint v2 argv (after the binary) for the union of
// mechanizable linters over the given package dirs. nil if either side is empty.
func LintArgs(sigs []Signal, dirs []string) []string {
	set := map[string]bool{}
	for _, s := range sigs {
		if s.Check.Kind != CheckGolangciLint {
			continue
		}
		for _, l := range s.Check.Linters {
			set[l] = true
		}
	}
	if len(set) == 0 || len(dirs) == 0 {
		return nil
	}
	linters := make([]string, 0, len(set))
	for l := range set {
		linters = append(linters, l)
	}
	sort.Strings(linters)
	args := []string{"run", "--no-config", "--default=none"}
	for _, l := range linters {
		args = append(args, "--enable="+l)
	}
	for _, d := range dirs {
		args = append(args, d+"/...")
	}
	return args
}
