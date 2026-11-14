package history_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
)

func TestRecordFixtures(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/record.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID            string
		Description   string
		Location      string
		RetentionDays int
		State         history.State
		Steps         []struct {
			Now    time.Time
			Sample history.Sample
			Period history.PeriodInput
			Want   history.State
		}
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID+" "+tc.Description, func(t *testing.T) {
			t.Parallel()

			location, err := time.LoadLocation(tc.Location)
			if err != nil {
				t.Fatal(err)
			}
			state := tc.State
			for i, step := range tc.Steps {
				before := clone(state)
				got := history.Record(state, step.Sample, step.Now, history.Settings{
					Location: location, RetentionDays: tc.RetentionDays, Period: step.Period,
				})
				if !reflect.DeepEqual(state, before) {
					t.Fatalf("step %d mutated input: got %+v; want %+v", i, state, before)
				}
				assertState(t, got, step.Want)
				state = got
			}
		})
	}
}

func TestRecordIgnoredSample(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	state := history.State{
		DailyUsage:    []history.DailyPoint{{Day: at.Add(-24 * time.Hour), Used: 60, DayStartUsed: 57}},
		RecentSamples: []history.Reading{{At: at, Used: 60}},
	}
	cases := []struct {
		name   string
		sample history.Sample
	}{
		{"stale value", history.Sample{At: at.Add(time.Hour), Used: 64, Fresh: false}},
		{"duplicate", history.Sample{At: at, Used: 60, Fresh: true}},
		{"duplicate timestamp with different value", history.Sample{At: at, Used: 64, Fresh: true}},
		{"older observation", history.Sample{At: at.Add(-time.Hour), Used: 59, Fresh: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := clone(state)
			// Even overdue retention must not change state on an ignored sample.
			got := history.Record(state, tc.sample, at.AddDate(0, 0, 10), history.Settings{
				Location: time.UTC, RetentionDays: 1,
			})
			if !reflect.DeepEqual(got, before) || !reflect.DeepEqual(state, before) {
				t.Fatalf("ignored sample changed state: got %+v; want %+v", got, before)
			}
		})
	}
}

func TestRecordDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 20, 10, 0, 0, 0, time.UTC)
	for _, sameDay := range []bool{true, false} {
		t.Run(map[bool]string{true: "update existing day", false: "append new day"}[sameDay], func(t *testing.T) {
			t.Parallel()

			day := time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
			if !sameDay {
				day = day.AddDate(0, 0, -1)
			}
			daily := []history.DailyPoint{{Day: day, Used: 60, DayStartUsed: 57}, {Used: 999}, {Used: 998}}
			samples := []history.Reading{{At: at.Add(-time.Hour), Used: 60}, {Used: 999}, {Used: 998}}
			dailyBefore, samplesBefore := slices.Clone(daily), slices.Clone(samples)
			state := history.State{DailyUsage: daily[:1], RecentSamples: samples[:1]}
			got := history.Record(state, history.Sample{At: at, Used: 64, Fresh: true}, at, history.Settings{
				Location: time.UTC, RetentionDays: 30,
			})
			if !reflect.DeepEqual(daily, dailyBefore) || !reflect.DeepEqual(samples, samplesBefore) {
				t.Fatal("Record mutated input backing arrays (including spare capacity)")
			}
			if sameDay && (len(got.DailyUsage) != 1 || got.DailyUsage[0].Used != 64) {
				t.Fatalf("existing daily point was not updated: %+v", got.DailyUsage)
			}
			got.DailyUsage[0].Used = 1000
			got.RecentSamples[0].Used = 1000
			if !reflect.DeepEqual(daily, dailyBefore) || !reflect.DeepEqual(samples, samplesBefore) {
				t.Fatal("returned state aliases input backing arrays")
			}
		})
	}
}

func clone(state history.State) history.State {
	return history.State{DailyUsage: slices.Clone(state.DailyUsage), RecentSamples: slices.Clone(state.RecentSamples)}
}

func assertState(t *testing.T, got, want history.State) {
	t.Helper()
	if len(got.DailyUsage) != len(want.DailyUsage) || len(got.RecentSamples) != len(want.RecentSamples) {
		t.Fatalf("state lengths: got %+v; want %+v", got, want)
	}
	for i, point := range got.DailyUsage {
		other := want.DailyUsage[i]
		if !point.Day.Equal(other.Day) || point.Used != other.Used || point.DayStartUsed != other.DayStartUsed {
			t.Fatalf("daily point %d: got %+v; want %+v", i, point, other)
		}
	}
	for i, sample := range got.RecentSamples {
		other := want.RecentSamples[i]
		if !sample.At.Equal(other.At) || sample.Used != other.Used {
			t.Fatalf("sample %d: got %+v; want %+v", i, sample, other)
		}
	}
}
