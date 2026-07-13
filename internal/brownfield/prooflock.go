package brownfield

import (
	"context"

	"github.com/revelara-ai/orion/internal/proof/prooflock"
)

// acquireProofLock blocks until this process holds the machine-wide toolchain
// lock (or-6wbl: two concurrent regression gates false-redded each other —
// queueing beats a false verdict). The lock now lives in
// internal/proof/prooflock (or-7y68) and is SHARED with the greenfield proof
// execs (proofexec), so a regression gate and a proof build mutually exclude
// too — previously each side only serialized against itself.
func acquireProofLock(ctx context.Context) (release func(), err error) {
	return prooflock.Acquire(ctx)
}
