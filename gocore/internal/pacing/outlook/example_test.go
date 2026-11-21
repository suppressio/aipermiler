package outlook_test

import (
	"fmt"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/outlook"
)

func ExampleTodayBudget() {
	// B-9: on the last working afternoon the whole remaining quota of the
	// morning is the day's budget.
	budget, _ := outlook.TodayBudget(58, true, 1, 1)
	fmt.Println(budget, outlook.UsedToday(60, 58))
	// Output: 42 2
}

func ExampleRedistribute() {
	// Q-1: 40 pp left over the 9 units from today on, against an even share
	// of 4.545.
	r, _ := outlook.Redistribute(40, 9, 22)
	fmt.Printf("%.3f %.3f %.3f\n", r.PerUnit, r.EvenShare, r.Ratio())
	// Output: 4.444 4.545 0.978
}

func ExampleJudge() {
	// V-6: the redistribution looks fine, but the pace is above 1.5 times
	// what is left per unit.
	r, _ := outlook.Redistribute(40, 9, 22)
	got := outlook.Judge(outlook.VerdictInput{Used: 60, Redistribution: &r, Summary: &outlook.Summary{Pace: 7, HasPace: true}})
	fmt.Println(got.Verdict, got.Reason)
	// Output: at-risk pace
}

func ExampleSummarize() {
	// P-1: 60% used, 5 pp/unit of recent pace.
	s := outlook.Summarize(60, 13.375, 8.625, 5, true)
	fmt.Printf("%.1f %.1f %v\n", s.Projection, s.Autonomy, s.Preliminary)
	// Output: 100.9 8.4 false
}
