package advice_test

import (
	"fmt"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/advice"
)

func ExampleTipCandidates() {
	// TP-2: nothing is advised on an exhausted quota, whatever else holds.
	tips := advice.TipCandidates(advice.TipInput{
		Used: 100, HasAutonomy: true, WorkingUnitsToReset: 8.6,
		Rating: &advice.Rating{Stars: 5, Average: 2}, DaysToReset: 1, HasDaysToReset: true,
	})
	fmt.Println(len(tips))
	// Output: 0
}

func ExampleTipCandidates_autonomy() {
	// TP-1: 8.4 working days of autonomy against 8.6 left: a 2 % reduction.
	tips := advice.TipCandidates(advice.TipInput{Used: 60, Autonomy: 8.4, HasAutonomy: true, WorkingUnitsToReset: 8.6})
	fmt.Printf("%s %.0f\n", tips[0].Key, tips[0].Params["reduction"])
	// Output: autonomy 2
}
