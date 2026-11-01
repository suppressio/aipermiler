package usage

import (
	"context"
	"encoding/json"
	"errors"
)

// Code is a stable machine-readable error category.
type Code string

// Stable provider error codes; CodeUsage is reserved for exit code 2.
const (
	CodeSignedOut   Code = "signed_out"
	CodeNotReady    Code = "not_ready"
	CodeTimeout     Code = "timeout"
	CodeFormatDrift Code = "format_drift"
	CodeUnavailable Code = "unavailable"
	CodeUsage       Code = "usage"
)

// Error carries a safe message and, for format drift, only response structure.
type Error struct {
	Code     Code
	Message  string
	Endpoint string
	Shape    any
	// Reason optionally names a known case inside the code (e.g.
	// "enterprise_managed_seat"), so a client can explain it in the user's language
	// without parsing Message. Additive in v1: absent when empty.
	Reason string
}

func (e *Error) Error() string { return e.Message }

// MarshalJSON exposes diagnostic fields only for format drift.
func (e *Error) MarshalJSON() ([]byte, error) {
	if e.Code == CodeFormatDrift {
		return json.Marshal(struct {
			Code     Code   `json:"code"`
			Message  string `json:"message"`
			Reason   string `json:"reason,omitempty"`
			Endpoint string `json:"endpoint"`
			Shape    any    `json:"shape"`
		}{e.Code, e.Message, e.Reason, e.Endpoint, e.Shape})
	}
	return json.Marshal(struct {
		Code    Code   `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason,omitempty"`
	}{e.Code, e.Message, e.Reason})
}

// ClassifyError preserves provider errors and recognizes wrapped deadlines.
func ClassifyError(err error) *Error {
	var providerError *Error
	if errors.As(err, &providerError) {
		return providerError
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Message: "Request timed out"}
	}
	return &Error{Code: CodeUnavailable, Message: err.Error()}
}

// ExtractShape replaces JSON values with type names, keeping one array element.
// Object keys are sorted by encoding/json when the resulting map is marshaled.
func ExtractShape(value any) any { return extractShape(value, 0) }

func extractShape(value any, depth int) any {
	if depth > 4 {
		return "truncated"
	}
	switch v := value.(type) {
	case nil:
		return "null"
	case map[string]any:
		shape := make(map[string]any, len(v))
		for key, item := range v {
			shape[key] = extractShape(item, depth+1)
		}
		return shape
	case []any:
		if len(v) == 0 {
			return []any{}
		}
		return []any{extractShape(v[0], depth+1)}
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}
