// Package usage defines the transport-neutral provider usage contract.
package usage

import (
	"encoding/json"
	"time"
)

// SchemaVersion changes only for incompatible contract changes.
const SchemaVersion = 1

// PeriodType describes how a quota window resets.
type PeriodType string

// Supported quota period types.
const (
	PeriodRollingHours PeriodType = "rolling-hours"
	PeriodRollingDays  PeriodType = "rolling-days"
	PeriodBillingCycle PeriodType = "billing-cycle"
)

// Unit describes the values reported by a quota window.
type Unit string

// Supported quota units.
const (
	UnitPercentage Unit = "percentage"
	UnitCount      Unit = "count"
)

// QuotaWindow mirrors the TypeScript QuotaWindow; nullable fields stay present.
type QuotaWindow struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	PeriodType   PeriodType `json:"periodType"`
	PeriodLength *float64   `json:"periodLength"`
	Unit         Unit       `json:"unit"`
	Used         float64    `json:"used"`
	Total        *float64   `json:"total"`
	ResetsAt     *time.Time `json:"-"`
}

// MarshalJSON normalizes reset dates to UTC at second precision.
func (q QuotaWindow) MarshalJSON() ([]byte, error) {
	type fields QuotaWindow
	return json.Marshal(struct {
		fields
		ResetsAt *string `json:"resetsAt"`
	}{fields: fields(q), ResetsAt: isoTime(q.ResetsAt)})
}

// StaleInfo explains why a provider returned last known values.
type StaleInfo struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Usage mirrors RawAccountUsage, with stale metadata kept outside its JSON body.
type Usage struct {
	PlanTier             *string       `json:"planTier"`
	SubscriptionRenewsAt *time.Time    `json:"-"`
	QuotaWindows         []QuotaWindow `json:"quotaWindows"`
	Stale                *StaleInfo    `json:"-"`
}

// MarshalJSON preserves null fields and encodes absent windows as an empty array.
func (u Usage) MarshalJSON() ([]byte, error) {
	windows := u.QuotaWindows
	if windows == nil {
		windows = []QuotaWindow{}
	}
	return json.Marshal(struct {
		PlanTier             *string       `json:"planTier"`
		SubscriptionRenewsAt *string       `json:"subscriptionRenewsAt"`
		QuotaWindows         []QuotaWindow `json:"quotaWindows"`
	}{u.PlanTier, isoTime(u.SubscriptionRenewsAt), windows})
}

func isoTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}
