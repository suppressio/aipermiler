package pace_test

import (
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/pace"
)

func ExampleUsed() {
	// An overrun stays visible: 103 is not clamped to 100.
	x, _ := pace.Used(103, 100, pace.Percentage)
	fmt.Println(x, pace.Remaining(x))
	// Output: 103 0
}

func ExampleTodayActivity() {
	// A-1: a rise of 0.03 pp is the provider's rounding, not work.
	at := func(hour int) time.Time { return time.Date(2026, time.October, 20, hour, 0, 0, 0, time.UTC) }
	baseline := 57.0
	samples := []history.Reading{{At: at(9), Used: 57.00}, {At: at(10), Used: 57.03}}
	_, ok := pace.TodayActivity(samples, &baseline, time.Time{}, at(15), time.UTC)
	fmt.Println(ok)
	// Output: false
}

func ExampleProjection() {
	// E2: in the last minutes the projection is not capped and barely moves.
	fmt.Printf("%.2f\n", pace.Projection(60, 4, true, 0.0125))
	// Output: 60.05
}

func ExampleAutonomy() {
	// No pace, no end: infinity, shown by the caller as "lasts to the reset".
	fmt.Println(pace.Autonomy(0, 0, false))
	// Output: +Inf
}
