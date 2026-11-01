package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/github"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// Port of services/copilot.ts (docs/RESEARCH.md §2). Two sources:
//   - copilot_internal/user, the undocumented endpoint behind the VS Code quota
//     indicator: real quotas per category, read with VS Code's rules; the only source
//     for company seats, the first one for personal plans;
//   - the official billing report (ai_credit/usage, then premium_request/usage), the
//     month's spend in USD against a hand-entered total; personal plans only.
// Window ids and labels are the TypeScript ones, so the stored history keeps matching.

const (
	usdPerCredit = 0.01 // 1 AI credit = $0.01 (docs/RESEARCH.md §2.1)
	// "This billing report does not apply to this account": 404 (until 2026-07) or, since
	// 2026-10, 400 with this message for accounts without their own billing.
	billingNotApplicableMessage = "Unable to get billing usage data"
	enterpriseManagedSKU        = "enterprise_managed"
	// InternalEndpointLabel names the main source in drift reports and the raw document.
	InternalEndpointLabel = "{api}/copilot_internal/user"
	aiCreditLabel         = "users/{username}/settings/billing/ai_credit/usage"
)

// Reasons inside the error codes (usage.Error.Reason).
const (
	ReasonNotConnected       = "not_connected"
	ReasonTokenRejected      = "token_rejected"
	ReasonEnterpriseManaged  = "enterprise_managed_seat"
	ReasonKeyringUnavailable = "keyring_unavailable"
)

type fetcher struct {
	client   *github.Client
	api      string
	token    string
	now      time.Time
	manual   *float64
	personal bool
}

// errEnterpriseManaged: GitHub exposes no usage for enterprise-managed seats (#7).
var errEnterpriseManaged = &usage.Error{
	Code:    usage.CodeUnavailable,
	Reason:  ReasonEnterpriseManaged,
	Message: "Copilot: GitHub exposes no usage data for enterprise-managed seats (no quota_snapshots) — see docs/RESEARCH.md §2.2",
}

func (f *fetcher) fetch(ctx context.Context) (usage.Usage, error) {
	if !f.personal {
		return f.internal(ctx, "company seat")
	}
	return f.personalUsage(ctx)
}

// personalUsage: the internal quotas first, the billing report as a fallback.
func (f *fetcher) personalUsage(ctx context.Context) (usage.Usage, error) {
	internal, internalErr := f.internal(ctx, "personal plan")
	if internalErr == nil && len(internal.QuotaWindows) > 0 {
		return internal, nil
	}
	billing, err := f.billing(ctx)
	if err == nil {
		return billing, nil
	}
	// A recognized internal answer with nothing to show (a Free plan with no premium
	// allotment) is a legitimate result, whatever the billing report says.
	if internalErr == nil {
		return internal, nil
	}
	// Neither source applies: the internal error says why (enterprise-managed seat,
	// drift). Any other billing error (401/403, network) is about the token: it wins.
	if billingNotApplicable(err) {
		return usage.Usage{}, internalErr
	}
	return usage.Usage{}, err
}

func billingNotApplicable(err error) bool {
	var httpErr *github.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.Status == http.StatusNotFound ||
		(httpErr.Status == http.StatusBadRequest && strings.Contains(httpErr.Message, billingNotApplicableMessage))
}

// Username resolves the GitHub login of the token (GET /user).
func Username(ctx context.Context, client *github.Client, api, token string) (string, error) {
	data, err := client.GetJSON(ctx, api+"/user", token, "GET /user")
	if err != nil {
		return "", err
	}
	user, _ := data.(map[string]any)
	login, _ := user["login"].(string)
	if login == "" {
		return "", errors.New("copilot: could not determine the username from the given token")
	}
	return login, nil
}

func (f *fetcher) billing(ctx context.Context) (usage.Usage, error) {
	username, err := Username(ctx, f.client, f.api, f.token)
	if err != nil {
		return usage.Usage{}, err
	}
	year, month := f.now.UTC().Year(), int(f.now.UTC().Month())
	query := fmt.Sprintf("?year=%d&month=%02d", year, month)
	base := f.api + "/users/" + url.PathEscape(username) + "/settings/billing/"
	report, err := f.client.GetJSON(ctx, base+"ai_credit/usage"+query, f.token, aiCreditLabel)
	if billingNotApplicable(err) {
		// Same response shape per the REST docs: same parsing.
		report, err = f.client.GetJSON(ctx, base+"premium_request/usage"+query, f.token, "users/{username}/settings/billing/premium_request/usage (fallback from ai_credit/usage)")
	}
	if err != nil {
		return usage.Usage{}, err
	}
	used, err := sumCreditsUsed(report)
	if err != nil {
		return usage.Usage{}, err
	}
	reset := BillingReportResetsAt(f.now)
	one := 1.0
	return usage.Usage{
		SubscriptionRenewsAt: &reset,
		QuotaWindows: []usage.QuotaWindow{{
			ID: "ai_credits", Label: "AI credits", PeriodType: usage.PeriodBillingCycle, PeriodLength: &one,
			Unit: usage.UnitCount, Used: used, Total: f.manual, ResetsAt: &reset,
		}},
	}, nil
}

// BillingReportResetsAt returns when the billing count restarts: the report covers the UTC calendar month it is queried for,
// so its count restarts on the 1st of the next month at 00:00 UTC.
func BillingReportResetsAt(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// sumCreditsUsed: the report's usage items in USD (netAmount) → AI credits.
func sumCreditsUsed(report any) (float64, error) {
	object, _ := report.(map[string]any)
	items, ok := object["usageItems"].([]any)
	if !ok {
		return 0, &usage.Error{
			Code:     usage.CodeFormatDrift,
			Message:  "Copilot: unexpected response format on ai_credit/usage (no usageItems) — see docs/RESEARCH.md",
			Endpoint: aiCreditLabel,
			Shape:    usage.ExtractShape(report),
		}
	}
	total := 0.0
	for _, item := range items {
		entry, _ := item.(map[string]any)
		total += numberOf(entry["netAmount"])
	}
	return math.Round(total / usdPerCredit), nil
}

// numberOf mirrors JavaScript's Number(x) || 0 for the values a JSON body can hold.
func numberOf(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n
		}
	}
	return 0
}

type internalResponse struct {
	AccessTypeSKU     any             `json:"access_type_sku"`
	CopilotPlan       string          `json:"copilot_plan"`
	QuotaResetDate    string          `json:"quota_reset_date"`
	QuotaResetDateUTC string          `json:"quota_reset_date_utc"`
	QuotaSnapshots    json.RawMessage `json:"quota_snapshots"`
}

func (f *fetcher) internalRaw(ctx context.Context) ([]byte, error) {
	return f.client.GetRaw(ctx, f.api+"/copilot_internal/user", f.token, "copilot_internal/user (unofficial internal endpoint)")
}

func (f *fetcher) internal(ctx context.Context, context string) (usage.Usage, error) {
	raw, err := f.internalRaw(ctx)
	if err != nil {
		return usage.Usage{}, fmt.Errorf("copilot (%s, best-effort): call failed — %w. This endpoint is not official: it may require a Copilot token different from a standard PAT. See docs/RESEARCH.md", context, err)
	}
	var decoded any
	_ = json.Unmarshal(raw, &decoded) // valid JSON: checked by GetRaw
	// The raw bytes keep the snapshots in their original key order (Go maps have none).
	var response internalResponse
	_ = json.Unmarshal(raw, &response)
	keys, snapshots := orderedObject(response.QuotaSnapshots)
	if snapshots == nil {
		if sku, _ := response.AccessTypeSKU.(string); sku == enterpriseManagedSKU {
			return usage.Usage{}, errEnterpriseManaged
		}
		return usage.Usage{}, &usage.Error{
			Code:     usage.CodeFormatDrift,
			Message:  fmt.Sprintf("Copilot (%s, best-effort): response without quota_snapshots — format changed or token not valid for this endpoint", context),
			Endpoint: "copilot_internal/user",
			Shape:    usage.ExtractShape(decoded),
		}
	}
	fallback := parseDate(response.QuotaResetDateUTC)
	if response.QuotaResetDateUTC == "" {
		fallback = parseDate(response.QuotaResetDate)
	}
	result := usage.Usage{SubscriptionRenewsAt: parseDate(response.QuotaResetDate)}
	if response.CopilotPlan != "" {
		plan := response.CopilotPlan
		result.PlanTier = &plan
	}
	recognized := false
	for _, key := range keys {
		window, kind := readSnapshot(key, snapshots[key], fallback)
		if kind == snapshotUnrecognized {
			continue
		}
		recognized = true
		if kind == snapshotWindow {
			result.QuotaWindows = append(result.QuotaWindows, window)
		}
	}
	if !recognized {
		var shapeSource any
		_ = json.Unmarshal(response.QuotaSnapshots, &shapeSource)
		return usage.Usage{}, &usage.Error{
			Code:     usage.CodeFormatDrift,
			Message:  fmt.Sprintf("Copilot (%s, best-effort): no quota window recognized in the response", context),
			Endpoint: "copilot_internal/user",
			Shape:    usage.ExtractShape(shapeSource),
		}
	}
	return result, nil
}

// orderedObject decodes a JSON object keeping its key order; nil when raw is not an
// object (absent, null, another type).
func orderedObject(raw json.RawMessage) ([]string, map[string]map[string]any) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil
	}
	var keys []string
	values := map[string]map[string]any{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, nil
		}
		key, _ := keyToken.(string)
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, nil
		}
		entry, _ := value.(map[string]any)
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = entry
	}
	return keys, values
}

// parseDate reads an ISO date-time or a bare date (UTC midnight, like new Date("YYYY-MM-DD")).
func parseDate(value string) *time.Time {
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	return nil
}

type snapshotKind int

const (
	snapshotUnrecognized snapshotKind = iota
	snapshotEmpty
	snapshotWindow
)

// readSnapshot: one quota snapshot → one window, with VS Code's rules (getQuotaUsage):
// unlimited → credits used only, nothing when there are none; entitlement > 0 →
// entitlement − quota_remaining of the entitlement; entitlement 0 → nothing allocated;
// otherwise the percentage, as before token-based billing.
func readSnapshot(key string, entry map[string]any, fallback *time.Time) (usage.QuotaWindow, snapshotKind) {
	percentRemaining, ok := entry["percent_remaining"].(float64)
	if !ok {
		return usage.QuotaWindow{}, snapshotUnrecognized
	}
	percentRemaining = math.Min(100, math.Max(0, percentRemaining))
	resetsAt := fallback
	if seconds, ok := entry["quota_reset_at"].(float64); ok && seconds > 0 {
		t := time.Unix(int64(seconds), 0).UTC()
		resetsAt = &t
	}
	one := 1.0
	window := usage.QuotaWindow{ID: key, Label: "Copilot — " + key, PeriodType: usage.PeriodBillingCycle, PeriodLength: &one, ResetsAt: resetsAt}

	if entry["unlimited"] == true {
		// Unlike VS Code, an unlimited category with zero credits is not shown: on a
		// company seat chat and completions are always unlimited and would only add rows.
		credits, ok := entry["credits_used"].(float64)
		if entry["has_quota"] == false || !ok || credits <= 0 {
			return usage.QuotaWindow{}, snapshotEmpty
		}
		window.Unit, window.Used = usage.UnitCount, credits
		return window, snapshotWindow
	}
	entitlement, hasEntitlement := entry["entitlement"].(float64)
	if hasEntitlement && entitlement == 0 {
		return usage.QuotaWindow{}, snapshotEmpty
	}
	if hasEntitlement && entitlement > 0 {
		used := entitlement * (100 - percentRemaining) / 100
		if remaining, ok := entry["quota_remaining"].(float64); ok {
			used = entitlement - remaining
		}
		total := entitlement
		// Rounded: entitlement − quota_remaining on fractional credits leaves float noise.
		window.Unit, window.Used, window.Total = usage.UnitCount, math.Max(0, math.Round(used*10000)/10000), &total
		return window, snapshotWindow
	}
	window.Unit, window.Used = usage.UnitPercentage, math.Round((100-percentRemaining)*10)/10
	return window, snapshotWindow
}
