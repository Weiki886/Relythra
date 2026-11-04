// Package prompt builds a versioned, allowlisted runtime context for model
// calls. Evaluation answers and injection metadata are outside this schema.
package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Weiki886/relythra/internal/domain"
	"github.com/Weiki886/relythra/internal/security/redact"
)

const (
	MaxContextBytes        = 1024 * 1024
	MaxAlertSummaryBytes   = 4096
	MaxEvidenceItems       = 32
	MaxEvidenceContentSize = 16 * 1024
	MaxChangeItems         = 20
	MaxChangeSummaryBytes  = 4096
	MaxDiffExcerptSize     = 32 * 1024
	MaxToolResultItems     = 32
	MaxToolSummarySize     = 8 * 1024
)

var (
	ErrInvalidContext = errors.New("invalid model context")

	repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitSHA      = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	environment    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	serviceName    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
	toolName       = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
)

type Context struct {
	Version       string               `json:"version"`
	Investigation InvestigationContext `json:"investigation"`
	Evidence      []EvidenceContext    `json:"evidence"`
	Changes       []ChangeContext      `json:"changes"`
	ToolResults   []ToolResultContext  `json:"tool_results"`
}

type InvestigationContext struct {
	ID             string               `json:"id"`
	Service        string               `json:"service"`
	Environment    string               `json:"environment"`
	AlertSummary   redact.UntrustedData `json:"alert_summary"`
	AlertStartedAt time.Time            `json:"alert_started_at"`
}

type EvidenceContext struct {
	ID         string               `json:"id"`
	SourceType string               `json:"source_type"`
	ObservedAt time.Time            `json:"observed_at"`
	Content    redact.UntrustedData `json:"content"`
}

type ChangeContext struct {
	Repository string               `json:"repository"`
	CommitSHA  string               `json:"commit_sha"`
	Summary    redact.UntrustedData `json:"summary"`
	Diff       redact.UntrustedData `json:"diff_excerpt"`
}

type ToolResultContext struct {
	ToolName string               `json:"tool_name"`
	Status   string               `json:"status"`
	Summary  redact.UntrustedData `json:"summary"`
}

type rawContext struct {
	Version       string           `json:"version"`
	Investigation rawInvestigation `json:"investigation"`
	Evidence      []rawEvidence    `json:"evidence"`
	Changes       []rawChange      `json:"changes"`
	ToolResults   []rawToolResult  `json:"tool_results"`
}

type rawInvestigation struct {
	ID             string    `json:"id"`
	Service        string    `json:"service"`
	Environment    string    `json:"environment"`
	AlertSummary   string    `json:"alert_summary"`
	AlertStartedAt time.Time `json:"alert_started_at"`
}

type rawEvidence struct {
	ID         string    `json:"id"`
	SourceType string    `json:"source_type"`
	ObservedAt time.Time `json:"observed_at"`
	Content    string    `json:"content"`
}

type rawChange struct {
	Repository  string `json:"repository"`
	CommitSHA   string `json:"commit_sha"`
	Summary     string `json:"summary"`
	DiffExcerpt string `json:"diff_excerpt"`
}

type rawToolResult struct {
	ToolName string `json:"tool_name"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
}

// DecodeContext rejects unknown fields, extra documents, oversized input, and
// values outside the runtime allowlist before any text can reach a model.
func DecodeContext(reader io.Reader, filter *redact.Filter) (Context, error) {
	if reader == nil || filter == nil {
		return Context{}, fmt.Errorf("%w: reader and filter are required", ErrInvalidContext)
	}
	limited := io.LimitReader(reader, MaxContextBytes+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return Context{}, fmt.Errorf("%w: input could not be read", ErrInvalidContext)
	}
	if len(encoded) > MaxContextBytes {
		return Context{}, fmt.Errorf("%w: input exceeds size limit", ErrInvalidContext)
	}

	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var raw rawContext
	if err := decoder.Decode(&raw); err != nil {
		return Context{}, fmt.Errorf("%w: schema does not match", ErrInvalidContext)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Context{}, fmt.Errorf("%w: exactly one JSON document is required", ErrInvalidContext)
	}
	if err := validateRawContext(raw); err != nil {
		return Context{}, err
	}
	if err := validateTrustedText(raw, filter); err != nil {
		return Context{}, err
	}

	ctx := context.Background()
	alertSummary, err := filter.Untrusted(
		ctx,
		"grafana_alert",
		raw.Investigation.AlertSummary,
		MaxAlertSummaryBytes,
	)
	if err != nil {
		return Context{}, fmt.Errorf("%w: alert summary could not be redacted", ErrInvalidContext)
	}
	result := Context{
		Version: raw.Version,
		Investigation: InvestigationContext{
			ID:             raw.Investigation.ID,
			Service:        raw.Investigation.Service,
			Environment:    raw.Investigation.Environment,
			AlertSummary:   alertSummary,
			AlertStartedAt: raw.Investigation.AlertStartedAt,
		},
		Evidence:    make([]EvidenceContext, 0, len(raw.Evidence)),
		Changes:     make([]ChangeContext, 0, len(raw.Changes)),
		ToolResults: make([]ToolResultContext, 0, len(raw.ToolResults)),
	}

	for _, evidence := range raw.Evidence {
		content, err := filter.Untrusted(
			ctx,
			"runtime_evidence",
			evidence.Content,
			MaxEvidenceContentSize,
		)
		if err != nil {
			return Context{}, fmt.Errorf("%w: evidence could not be redacted", ErrInvalidContext)
		}
		result.Evidence = append(result.Evidence, EvidenceContext{
			ID:         evidence.ID,
			SourceType: evidence.SourceType,
			ObservedAt: evidence.ObservedAt,
			Content:    content,
		})
	}
	for _, change := range raw.Changes {
		summary, err := filter.Untrusted(
			ctx,
			"github_change_summary",
			change.Summary,
			MaxChangeSummaryBytes,
		)
		if err != nil {
			return Context{}, fmt.Errorf("%w: change summary could not be redacted", ErrInvalidContext)
		}
		diff, err := filter.Untrusted(
			ctx,
			"github_diff",
			change.DiffExcerpt,
			MaxDiffExcerptSize,
		)
		if err != nil {
			return Context{}, fmt.Errorf("%w: diff could not be redacted", ErrInvalidContext)
		}
		result.Changes = append(result.Changes, ChangeContext{
			Repository: change.Repository,
			CommitSHA:  change.CommitSHA,
			Summary:    summary,
			Diff:       diff,
		})
	}
	for _, toolResult := range raw.ToolResults {
		summary, err := filter.Untrusted(
			ctx,
			"tool_result",
			toolResult.Summary,
			MaxToolSummarySize,
		)
		if err != nil {
			return Context{}, fmt.Errorf("%w: tool result could not be redacted", ErrInvalidContext)
		}
		result.ToolResults = append(result.ToolResults, ToolResultContext{
			ToolName: toolResult.ToolName,
			Status:   toolResult.Status,
			Summary:  summary,
		})
	}
	return result, nil
}

func validateRawContext(raw rawContext) error {
	if raw.Version != "v1" {
		return fmt.Errorf("%w: version must be v1", ErrInvalidContext)
	}
	if !domain.IsID(raw.Investigation.ID) ||
		!serviceName.MatchString(raw.Investigation.Service) ||
		!environment.MatchString(raw.Investigation.Environment) ||
		strings.TrimSpace(raw.Investigation.AlertSummary) == "" ||
		len(raw.Investigation.AlertSummary) > MaxAlertSummaryBytes ||
		raw.Investigation.AlertStartedAt.IsZero() {
		return fmt.Errorf("%w: investigation fields are invalid", ErrInvalidContext)
	}
	if len(raw.Evidence) > MaxEvidenceItems ||
		len(raw.Changes) > MaxChangeItems ||
		len(raw.ToolResults) > MaxToolResultItems {
		return fmt.Errorf("%w: collection exceeds item limit", ErrInvalidContext)
	}

	for _, evidence := range raw.Evidence {
		if !domain.IsID(evidence.ID) ||
			!allowedEvidenceSource(evidence.SourceType) ||
			evidence.ObservedAt.IsZero() ||
			len(evidence.Content) > MaxEvidenceContentSize {
			return fmt.Errorf("%w: evidence fields are invalid", ErrInvalidContext)
		}
	}
	for _, change := range raw.Changes {
		if !repositoryName.MatchString(change.Repository) ||
			!commitSHA.MatchString(change.CommitSHA) ||
			len(change.Summary) > MaxChangeSummaryBytes ||
			len(change.DiffExcerpt) > MaxDiffExcerptSize {
			return fmt.Errorf("%w: change fields are invalid", ErrInvalidContext)
		}
	}
	for _, toolResult := range raw.ToolResults {
		if !toolName.MatchString(toolResult.ToolName) ||
			!allowedToolStatus(toolResult.Status) ||
			len(toolResult.Summary) > MaxToolSummarySize {
			return fmt.Errorf("%w: tool result fields are invalid", ErrInvalidContext)
		}
	}
	return nil
}

func validateTrustedText(raw rawContext, filter *redact.Filter) error {
	values := []string{
		raw.Version,
		raw.Investigation.ID,
		raw.Investigation.Service,
		raw.Investigation.Environment,
	}
	for _, evidence := range raw.Evidence {
		values = append(values, evidence.ID, evidence.SourceType)
	}
	for _, change := range raw.Changes {
		values = append(values, change.Repository, change.CommitSHA)
	}
	for _, toolResult := range raw.ToolResults {
		values = append(values, toolResult.ToolName, toolResult.Status)
	}
	for _, value := range values {
		safe, err := filter.Text(context.Background(), value)
		if err != nil || safe != value {
			return fmt.Errorf("%w: trusted identifiers contain sensitive data", ErrInvalidContext)
		}
	}
	return nil
}

func allowedEvidenceSource(value string) bool {
	switch value {
	case "DEPLOYMENT", "GITHUB", "PROMETHEUS", "LOKI", "RUNTIME", "MAPPING", "SYSTEM":
		return true
	default:
		return false
	}
}

func allowedToolStatus(value string) bool {
	switch value {
	case "STARTED", "SUCCEEDED", "FAILED":
		return true
	default:
		return false
	}
}
