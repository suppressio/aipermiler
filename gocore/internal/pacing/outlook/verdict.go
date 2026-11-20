package outlook

// Verdict is the reading of a window against its quota (§3.10).
type Verdict string

// Verdicts.
const (
	Exhausted Verdict = "exhausted"
	AtRisk    Verdict = "at-risk"
	Behind    Verdict = "behind"
	OnTrack   Verdict = "on-track"
	Ahead     Verdict = "ahead"
	NoPacing  Verdict = "no-pacing"
)

// Reason is which rule of §3.10 decided the verdict.
type Reason string

// Reasons.
const (
	ReasonNone       Reason = ""
	ReasonHourly     Reason = "hourly"
	ReasonPace       Reason = "pace"
	ReasonRatio      Reason = "ratio"
	ReasonAutonomy   Reason = "autonomy"
	ReasonProjection Reason = "projection"
)

// Bands of the ratio q / s (§3.10 step 3b).
const (
	atRiskRatio = 0.5
	behindRatio = 0.95
	aheadRatio  = 1.05
)

// VerdictInput gathers what the verdict reads.
type VerdictInput struct {
	// Used is x, not clamped.
	Used float64

	// Hourly is set for rolling-hours windows.
	Hourly *Hourly

	// Redistribution is set when q and s are defined.
	Redistribution *Redistribution

	// Summary is the projection summary of §3.13 (pace, preliminary,
	// projection, autonomy, R); nil when the window has none.
	Summary *Summary
}

// Judgement is the verdict with the rule behind it and, for a rolling-hours
// window at risk, the autonomy in hours.
type Judgement struct {
	Verdict       Verdict
	Reason        Reason
	AutonomyHours float64
	HasAutonomy   bool
}

// Judge evaluates the verdict of §3.10: the first rule that applies wins.
func Judge(in VerdictInput) Judgement {
	// 1.
	if in.Used >= 100 {
		return Judgement{Verdict: Exhausted}
	}
	// 2. A rolling-hours window with the reset passed falls through to 5: its
	// other values are stale too (E5).
	if h := in.Hourly; h != nil {
		if h.ResetPassed {
			return Judgement{Verdict: NoPacing}
		}
		if h.ProjectedAtReset > 100 {
			return Judgement{Verdict: AtRisk, Reason: ReasonHourly, AutonomyHours: h.AutonomyHours, HasAutonomy: h.HasAutonomy}
		}
		return Judgement{Verdict: OnTrack, Reason: ReasonHourly}
	}
	s := in.Summary
	hasPace := s != nil && s.HasPace
	// 3.
	if r := in.Redistribution; r != nil && r.EvenShare > 0 {
		if hasPace && !s.Preliminary && s.Pace > PaceAlertRatio*r.PerUnit {
			return Judgement{Verdict: AtRisk, Reason: ReasonPace}
		}
		switch ratio := r.Ratio(); {
		case ratio < atRiskRatio:
			return Judgement{Verdict: AtRisk, Reason: ReasonRatio}
		case ratio < behindRatio:
			return Judgement{Verdict: Behind, Reason: ReasonRatio}
		case ratio <= aheadRatio:
			return Judgement{Verdict: OnTrack, Reason: ReasonRatio}
		default:
			return Judgement{Verdict: Ahead, Reason: ReasonRatio}
		}
	}
	// 4.
	if hasPace {
		switch {
		case s.HasAutonomy && s.Autonomy < s.WorkingUnitsToReset:
			return Judgement{Verdict: AtRisk, Reason: ReasonAutonomy}
		case s.Projection > 100:
			return Judgement{Verdict: AtRisk, Reason: ReasonProjection}
		default:
			return Judgement{Verdict: OnTrack}
		}
	}
	// 5.
	return Judgement{Verdict: NoPacing}
}
