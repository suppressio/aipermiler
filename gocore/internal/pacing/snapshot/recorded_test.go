package snapshot_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/snapshot"
)

// A fresh read the refresh loop already recorded (the service's snapshot of
// its lastGood, #326) leaves the history as it is: history.Record ignores a
// sample no newer than the last one kept, so building the snapshot again is no
// second write (I-12).
func TestBuildOfAReadAlreadyRecordedKeepsTheHistory(t *testing.T) {
	t.Parallel()
	now := at(t, time.October, 20, 10, 0)
	read := now.Add(-5 * time.Minute)
	in := scenario(t, now, 34)
	in.Response.FetchedAt = read
	day := startOfDay(now, in.Settings.Location)
	in.History["monthly"] = history.State{
		DailyUsage: []history.DailyPoint{
			{Day: day.AddDate(0, 0, -1), Used: 30, DayStartUsed: 28},
			{Day: day, Used: 34, DayStartUsed: 30},
		},
		RecentSamples: []history.Reading{{At: read.Add(-30 * time.Minute), Used: 32}, {At: read, Used: 34}},
	}
	want := map[string]history.State{"monthly": in.History["monthly"]}
	if got := snapshot.Build(in).History; !reflect.DeepEqual(got, want) {
		t.Errorf("history = %+v; want the input %+v", got, want)
	}
}
