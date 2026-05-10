package stats

// BonferroniAdjust returns the per-axis confidence level when overall
// confidence overallLevel is to be maintained across numAxes axes.
//
// If you want overall α = 0.05 across 4 axes, each axis gets
// α' = 0.05/4 = 0.0125, i.e. confidence 0.9875.
func BonferroniAdjust(overallLevel float64, numAxes int) (float64, error) {
	if overallLevel <= 0 || overallLevel >= 1 {
		return 0, ErrInvalidLevel
	}
	if numAxes <= 0 {
		return 0, ErrInvalidLooks
	}
	overallAlpha := 1.0 - overallLevel
	perAxisAlpha := overallAlpha / float64(numAxes)
	return 1.0 - perAxisAlpha, nil
}
