package scoring

import (
	"fmt"
	"math"
)

// Small helpers shared by the package's tests.

// itoa renders an int without importing strconv at each call site.
func itoa(i int) string { return fmt.Sprintf("%d", i) }

// nan returns a NaN float64.
func nan() float64 { return math.NaN() }

// inf returns +Inf or -Inf depending on sign.
func inf(sign int) float64 {
	if sign < 0 {
		return math.Inf(-1)
	}
	return math.Inf(1)
}

// almostEqual compares floats with a tolerance, for formula results where exact
// equality is not meaningful.
func almostEqual(a, b, tolerance float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tolerance
}
