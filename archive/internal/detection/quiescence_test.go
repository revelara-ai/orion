package detection

import "testing"

func TestQuiescenceCheck(t *testing.T) {
	cases := []struct {
		name          string
		eligibleDepth int
		scanFindings  int
		want          bool
	}{
		{"empty backlog, empty scan", 0, 0, true},
		{"empty backlog, scan finds gaps", 0, 1, false},
		{"empty backlog, scan finds many", 0, 5, false},
		{"backlog present, empty scan", 5, 0, false},
		{"backlog present, scan finds gaps", 5, 1, false},
		{"both populated", 20, 3, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := QuiescenceCheck(c.eligibleDepth, c.scanFindings)
			if got != c.want {
				t.Errorf("eligibleDepth=%d scanFindings=%d: got %v, want %v",
					c.eligibleDepth, c.scanFindings, got, c.want)
			}
		})
	}
}
