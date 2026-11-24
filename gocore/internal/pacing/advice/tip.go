package advice

import "math"

// TipKey names a tip; the renderer owns the sentence (tips are data).
type TipKey string

// The tip keys, as the renderer's dictionary names them.
const (
	TipAutonomy       TipKey = "autonomy"
	TipRating         TipKey = "rating"
	TipNearReset      TipKey = "nearReset"
	TipNearResetToday TipKey = "nearResetToday"
	TipCause          TipKey = "cause"
)

// Tip is a key with the exact numbers it states.
type Tip struct {
	Key    TipKey
	Params map[string]float64
}

// Cause is a clear consumption cause (§3.15), as the local insights state it.
type Cause struct {
	HighPercent, LowPercent float64
	Days                    int
}

// TipInput holds the readings a tip can rest on. Used is the utilization x.
type TipInput struct {
	Used        float64
	Preliminary bool
	// Autonomy and WorkingUnitsToReset are a and R; HasAutonomy is false when
	// the autonomy is unknown or infinite.
	Autonomy            float64
	HasAutonomy         bool
	WorkingUnitsToReset float64
	Rating              *Rating
	// DaysToReset is the days to the reset; HasDaysToReset is false when the
	// reset is unknown.
	DaysToReset    float64
	HasDaysToReset bool
	Cause          *Cause
}

const (
	nearResetDays  = 2
	nearResetUsed  = 70
	ratingTipStars = 4
	minReduction   = 1
	maxReduction   = 99
	exhaustedUsed  = 100
)

// TipCandidates returns the tips whose condition holds (§3.14). An exhausted
// quota has none at all: the verdict already says so (D3, I-3).
func TipCandidates(in TipInput) []Tip {
	if in.Used >= exhaustedUsed {
		return nil
	}
	var tips []Tip
	if tip, ok := autonomyTip(in); ok {
		tips = append(tips, tip)
	}
	if in.Rating != nil && in.Rating.Stars >= ratingTipStars {
		tips = append(tips, Tip{Key: TipRating, Params: map[string]float64{
			"stars": float64(in.Rating.Stars), "average": in.Rating.Average,
		}})
	}
	if in.HasDaysToReset && in.DaysToReset <= nearResetDays && in.Used >= nearResetUsed {
		key := TipNearReset
		if in.DaysToReset < 1 {
			key = TipNearResetToday
		}
		tips = append(tips, Tip{Key: key, Params: map[string]float64{"days": in.DaysToReset, "used": in.Used}})
	}
	if in.Cause != nil {
		tips = append(tips, Tip{Key: TipCause, Params: map[string]float64{
			"highPercent": in.Cause.HighPercent, "lowPercent": in.Cause.LowPercent, "days": float64(in.Cause.Days),
		}})
	}
	return tips
}

// autonomyTip applies the bounds of I-3: the reduction as displayed (rounded
// to an integer) must be between 1 % and 99 %, so the tip never asks for no
// change or for the impossible. The parameters stay exact (I-4).
func autonomyTip(in TipInput) (Tip, bool) {
	if in.Preliminary || !in.HasAutonomy || in.Autonomy <= 0 || in.WorkingUnitsToReset <= 0 || in.Autonomy >= in.WorkingUnitsToReset {
		return Tip{}, false
	}
	reduction := (1 - in.Autonomy/in.WorkingUnitsToReset) * 100
	if shown := math.Round(reduction); shown < minReduction || shown > maxReduction {
		return Tip{}, false
	}
	return Tip{Key: TipAutonomy, Params: map[string]float64{
		"autonomy": in.Autonomy, "workingUnitsToReset": in.WorkingUnitsToReset, "reduction": reduction,
	}}, true
}

// PickTip chooses one candidate with a draw in [0, 1) (variety without ever
// stating something false); false when there is none. A draw outside the range
// is clamped to the last candidate.
func PickTip(candidates []Tip, draw float64) (Tip, bool) {
	if len(candidates) == 0 {
		return Tip{}, false
	}
	index := min(len(candidates)-1, max(0, int(math.Floor(draw*float64(len(candidates))))))
	return candidates[index], true
}
