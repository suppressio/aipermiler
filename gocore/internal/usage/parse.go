package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrBadDocument means the output is not one usage document of this contract
// version: a broken or incompatible core, never a provider's answer.
var ErrBadDocument = errors.New("not a usage document of contract v1")

// Document is one parsed usage document: Usage (with Stale set when the
// provider gave last known values) on success, Err for an error document.
type Document struct {
	Provider  string
	FetchedAt time.Time
	Usage     Usage
	Err       *Error
}

type wireWindow struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	PeriodType   PeriodType `json:"periodType"`
	PeriodLength *float64   `json:"periodLength"`
	Unit         Unit       `json:"unit"`
	Used         float64    `json:"used"`
	Total        *float64   `json:"total"`
	ResetsAt     *time.Time `json:"resetsAt"`
}

type wireDocument struct {
	SchemaVersion int        `json:"schemaVersion"`
	Provider      string     `json:"provider"`
	FetchedAt     *time.Time `json:"fetchedAt"`
	Stale         bool       `json:"stale"`
	StaleReason   *StaleInfo `json:"staleReason"`
	Usage         *struct {
		PlanTier             *string      `json:"planTier"`
		SubscriptionRenewsAt *time.Time   `json:"subscriptionRenewsAt"`
		QuotaWindows         []wireWindow `json:"quotaWindows"`
	} `json:"usage"`
	Error *struct {
		Code     Code   `json:"code"`
		Message  string `json:"message"`
		Reason   string `json:"reason"`
		Endpoint string `json:"endpoint"`
		Shape    any    `json:"shape"`
	} `json:"error"`
}

// ParseDocument reads exactly one usage document, as `usage --json` writes it
// (WriteSuccess, WriteError).
func ParseDocument(data []byte) (Document, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var wire wireDocument
	if err := decoder.Decode(&wire); err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrBadDocument, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Document{}, fmt.Errorf("%w: more than one document", ErrBadDocument)
	}
	if wire.SchemaVersion != SchemaVersion {
		return Document{}, fmt.Errorf("%w: schemaVersion %d", ErrBadDocument, wire.SchemaVersion)
	}
	doc := Document{Provider: wire.Provider}
	if wire.Error != nil {
		if wire.Error.Code == "" {
			return Document{}, fmt.Errorf("%w: error without a code", ErrBadDocument)
		}
		e := wire.Error
		doc.Err = &Error{Code: e.Code, Message: e.Message, Reason: e.Reason, Endpoint: e.Endpoint, Shape: e.Shape}
		return doc, nil
	}
	switch {
	case wire.FetchedAt == nil:
		return Document{}, fmt.Errorf("%w: no fetchedAt", ErrBadDocument)
	case wire.Usage == nil:
		return Document{}, fmt.Errorf("%w: no usage", ErrBadDocument)
	case wire.Stale && wire.StaleReason == nil:
		return Document{}, fmt.Errorf("%w: stale without a staleReason", ErrBadDocument)
	}
	doc.FetchedAt = *wire.FetchedAt
	doc.Usage = Usage{PlanTier: wire.Usage.PlanTier, SubscriptionRenewsAt: wire.Usage.SubscriptionRenewsAt}
	if wire.Stale {
		doc.Usage.Stale = wire.StaleReason
	}
	for _, w := range wire.Usage.QuotaWindows {
		doc.Usage.QuotaWindows = append(doc.Usage.QuotaWindows, QuotaWindow(w))
	}
	return doc, nil
}
