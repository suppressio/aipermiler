package advice_test

import (
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
)

// R-5: the edges of the star bands, on the exact average (I-4).
func TestStarsEdges(t *testing.T) {
	t.Parallel()

	for average, want := range map[float64]int{1.5: 5, 1.4999: 4, 1.1: 4, 1.0999: 3, 0.9: 3, 0.8999: 2, 0.6: 2, 0.59: 1, 0: 1, 3: 5} {
		if got := advice.Stars(average); got != want {
			t.Errorf("average %v = %d stars, want %d", average, got, want)
		}
	}
}
