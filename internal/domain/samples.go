package domain

import "math"

// Finite reports whether a sample has usable numeric values.
func (s Sample) Finite() bool {
	return math.IsNaN(s.TimeOffset) == false && math.IsInf(s.TimeOffset, 0) == false && math.IsNaN(s.FrequencyOffset) == false && math.IsInf(s.FrequencyOffset, 0) == false
}
