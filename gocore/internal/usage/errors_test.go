package usage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

func TestExtractShape(t *testing.T) {
	t.Parallel()
	deep := any(map[string]any{"leaf": 1})
	for range 20 {
		deep = map[string]any{"nested": deep}
	}
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"scalars", map[string]any{"used_dollars": 389.19, "label": "Cinder Cove", "active": true, "resets_at": nil}, `{"active":"boolean","label":"string","resets_at":"null","used_dollars":"number"}`},
		{"no real values", map[string]any{"used_dollars": 389.19, "limit_dollars": 1000}, `{"limit_dollars":"number","used_dollars":"number"}`},
		{"sorted keys", map[string]any{"b": 1, "a": 2}, `{"a":"number","b":"number"}`},
		{"representative array", map[string]any{"items": []any{map[string]any{"quantity": 1}, map[string]any{"quantity": 2}, map[string]any{"quantity": 3}}}, `{"items":[{"quantity":"number"}]}`},
		{"empty array", map[string]any{"items": []any{}}, `{"items":[]}`},
		{"depth limit", deep, `{"nested":{"nested":{"nested":{"nested":{"nested":"truncated"}}}}}`},
		{"nested objects", map[string]any{"quota_snapshots": map[string]any{"five_hour": map[string]any{"percent_remaining": 61.5}}}, `{"quota_snapshots":{"five_hour":{"percent_remaining":"number"}}}`},
		{"null", nil, `"null"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(usage.ExtractShape(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("shape = %s; want %s", got, tt.want)
			}
		})
	}
	a := usage.ExtractShape(map[string]any{"b": 1, "a": 2})
	b := usage.ExtractShape(map[string]any{"a": 20, "b": 10})
	if !reflect.DeepEqual(a, b) {
		t.Error("shape depends on values or key order")
	}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()
	providerErr := &usage.Error{Code: usage.CodeSignedOut, Message: "Sign in required"}
	tests := []struct {
		name    string
		err     error
		code    usage.Code
		message string
		same    *usage.Error
	}{
		{"provider", providerErr, usage.CodeSignedOut, providerErr.Message, providerErr},
		{"wrapped provider", fmt.Errorf("fetch: %w", providerErr), usage.CodeSignedOut, providerErr.Message, providerErr},
		{"deadline", context.DeadlineExceeded, usage.CodeTimeout, "Request timed out", nil},
		{"wrapped deadline", fmt.Errorf("fetch: %w", context.DeadlineExceeded), usage.CodeTimeout, "Request timed out", nil},
		{"other", errors.New("Connection failed"), usage.CodeUnavailable, "Connection failed", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usage.ClassifyError(tt.err)
			if got.Code != tt.code || got.Message != tt.message {
				t.Errorf("ClassifyError = %+v", got)
			}
			if tt.same != nil && got != tt.same {
				t.Error("provider error identity lost")
			}
		})
	}
}

func TestErrorDiagnosticsOnlyForDrift(t *testing.T) {
	t.Parallel()
	for _, code := range []usage.Code{usage.CodeSignedOut, usage.CodeNotReady, usage.CodeTimeout, usage.CodeFormatDrift, usage.CodeUnavailable, usage.CodeUsage} {
		t.Run(string(code), func(t *testing.T) {
			t.Parallel()
			e := &usage.Error{Code: code, Message: "Safe message", Endpoint: "/usage", Shape: usage.ExtractShape(map[string]any{"used": 42})}
			if e.Error() != "Safe message" {
				t.Error("wrong error message")
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{`"endpoint"`, `"shape"`} {
				if strings.Contains(string(data), key) != (code == usage.CodeFormatDrift) {
					t.Errorf("unexpected diagnostics: %s", data)
				}
			}
		})
	}
}
