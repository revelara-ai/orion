package lang

// pythonAdapter registers Python in the language capability manifest
// (or-4y7.9). Registration IS capability (or-4y7 invariant): with this init,
// completeness.provableFor("direction.language") includes "python", so a
// ratified python direction is no longer refused — every proof subsystem
// (proofexec toolchain, diagnostics, behavioral prover, empirical tool, hazard
// prober, generation adapter, export manifest) registered its python arm in
// lockstep. Scope of this tracer: LIBRARY artifacts proven through unit + file
// obligations; behavioral mutation is declared-unsupported (REDUCED label) and
// wireup is honestly Unverified.
type pythonAdapter struct{}

func (pythonAdapter) Language() string { return "python" }

func init() { Register(pythonAdapter{}) }
