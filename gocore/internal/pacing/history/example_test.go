package history_test

import (
	"fmt"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
)

func ExampleRecord() {
	// E11: consumption before the first refresh is known only on the start day.
	now := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	sample := history.Sample{At: now, Used: 57.0449, Fresh: true}
	settings := history.Settings{Location: time.UTC, RetentionDays: 30}
	settings.Period.Start = now.Add(-time.Hour)
	startDay := history.Record(history.State{}, sample, now, settings)
	settings.Period.Start = now.AddDate(0, 0, -1)
	midPeriod := history.Record(history.State{}, sample, now, settings)
	fmt.Println(startDay.DailyUsage[0].DayStartUsed)
	fmt.Println(midPeriod.DailyUsage[0].DayStartUsed)
	// Output:
	// 0
	// 57.0449
}

func ExampleRecord_staleSample() {
	now := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	state := history.Record(history.State{}, history.Sample{At: now, Used: 60, Fresh: false}, now,
		history.Settings{Location: time.UTC, RetentionDays: 30})
	fmt.Println(len(state.DailyUsage), len(state.RecentSamples))
	// Output: 0 0
}
