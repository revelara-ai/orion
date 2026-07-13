package contextstore

import "context"

// AdvanceSplit advances a spec-of-specs after a child project delivers
// (or-045a.4). If the delivered project is not a child, it is a no-op. While
// queued siblings remain, the oldest one takes the active slot (the sub-specs
// chain through the single-active queue without ever re-activating the parent
// umbrella). When EVERY child has delivered, the parent rolls up to delivered
// — through SetStatus, so the roll-up gate re-verifies. A sibling that is
// neither queued nor delivered (abandoned, or unexpectedly active) stops both
// the chain and the roll-up: the split needs the developer, not a guess.
func (s *Store) AdvanceSplit(ctx context.Context, childID string) (Project, bool, error) {
	var next Project
	rolledUp := false
	err := s.WithTx(ctx, func(tx *Tx) error {
		child, err := tx.Projects().Get(ctx, childID)
		if err != nil {
			return err
		}
		if child.ParentProjectID == "" {
			return nil // a flat project: nothing to advance
		}
		siblings, err := tx.Projects().ChildrenOf(ctx, child.ParentProjectID)
		if err != nil {
			return err
		}
		allDelivered := true
		for _, sib := range siblings {
			if sib.Status == "queued" {
				if err := tx.Projects().SetStatus(ctx, sib.ID, "active"); err != nil {
					return err
				}
				sib.Status = "active"
				next = sib
				return nil
			}
			if sib.Status != "delivered" {
				allDelivered = false
			}
		}
		if !allDelivered {
			return nil // e.g. an abandoned sibling: the roll-up is blocked, honestly
		}
		if err := tx.Projects().SetStatus(ctx, child.ParentProjectID, "delivered"); err != nil {
			return err
		}
		parent, err := tx.Projects().Get(ctx, child.ParentProjectID)
		if err != nil {
			return err
		}
		next, rolledUp = parent, true
		return nil
	})
	return next, rolledUp, err
}
