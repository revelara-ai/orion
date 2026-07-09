package brownfield

// Progress receives step transitions and per-package completions from the
// regression gates while they run — the heartbeat that keeps a long `go test`
// visibly alive in whatever surface hosts the gate (TUI activity panel, CLI).
// A 10-minute silent gate is indistinguishable from a hang (or-m45w); these
// events are how the surface tells "proving" from "dead".
//
// step is a stable label ("apply-change", "scope", "green-before",
// "green-after"); detail is the human payload ("testmod/a ok (1/2)"). nil is a
// valid sink.
type Progress func(step, detail string)

func (p Progress) emit(step, detail string) {
	if p != nil {
		p(step, detail)
	}
}
