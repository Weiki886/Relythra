// Package diagnosiscase defines the versioned evaluation-case contract.
// Evaluation truth is kept outside the runtime database and prompt DTOs.
package diagnosiscase

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	ErrDuplicateVersion = errors.New("duplicate diagnosis case version")
	ErrInvalidCaseSet   = errors.New("invalid diagnosis case set")
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Case struct {
	CaseID               string          `json:"case_id"`
	Version              int             `json:"version"`
	ScenarioType         string          `json:"scenario_type"`
	Service              string          `json:"service"`
	Environment          string          `json:"environment"`
	BaselineVersion      string          `json:"baseline_version"`
	FaultVersion         string          `json:"fault_version"`
	Window               TimeWindow      `json:"window"`
	Repetitions          int             `json:"repetitions"`
	RandomizeOrder       bool            `json:"randomize_order"`
	ExpectedRootCause    RootCause       `json:"expected_root_cause"`
	AllowedVariants      []string        `json:"allowed_variants"`
	RequiredEvidence     []EvidenceRule  `json:"required_evidence"`
	Counterexamples      []string        `json:"counterexamples"`
	ForbiddenConclusions []string        `json:"forbidden_conclusions"`
	HealthyBaseline      json.RawMessage `json:"healthy_baseline"`
	Injection            Procedure       `json:"injection"`
	Recovery             Procedure       `json:"recovery"`
	ExpectedTelemetry    []string        `json:"expected_telemetry"`
	ExpectedChanges      []string        `json:"expected_changes"`
	CleanupRequirements  []string        `json:"cleanup_requirements"`
	Scoring              ScoringRules    `json:"scoring"`
}

type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type RootCause struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

type EvidenceRule struct {
	Source       string `json:"source"`
	Predicate    string `json:"predicate"`
	TimeRelation string `json:"time_relation"`
}

type Procedure struct {
	Steps       []string `json:"steps"`
	StateMarker string   `json:"state_marker,omitempty"`
	Cleanup     []string `json:"cleanup,omitempty"`
}

type ScoringRules struct {
	Version string   `json:"version"`
	Metrics []string `json:"metrics"`
}

// RuntimeContext is an allowlist projection. Evaluation identifiers and truth
// data intentionally have no representation in this type.
type RuntimeContext struct {
	Service        string    `json:"service"`
	Environment    string    `json:"environment"`
	AlertStartedAt time.Time `json:"alert_started_at"`
}

func (caseFile Case) RuntimeContext() RuntimeContext {
	return RuntimeContext{
		Service:        caseFile.Service,
		Environment:    caseFile.Environment,
		AlertStartedAt: caseFile.Window.Start,
	}
}

func Decode(data []byte) (Case, error) {
	var caseFile Case
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&caseFile); err != nil {
		return Case{}, fmt.Errorf("decode diagnosis case: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Case{}, errors.New("diagnosis case contains trailing JSON document")
	}
	if err := caseFile.Validate(); err != nil {
		return Case{}, err
	}
	return caseFile, nil
}

func (caseFile Case) Validate() error {
	issues := make([]string, 0)
	if !identifierPattern.MatchString(caseFile.CaseID) {
		issues = append(issues, "case_id: must be a lowercase slug")
	}
	if caseFile.Version < 1 {
		issues = append(issues, "version: must be positive")
	}
	switch caseFile.ScenarioType {
	case "fault", "healthy_control", "interference":
	default:
		issues = append(issues, "scenario_type: unsupported value")
	}
	if !identifierPattern.MatchString(caseFile.Service) {
		issues = append(issues, "service: must be a lowercase slug")
	}
	if !identifierPattern.MatchString(caseFile.Environment) {
		issues = append(issues, "environment: must be a lowercase slug")
	}
	if strings.TrimSpace(caseFile.BaselineVersion) == "" {
		issues = append(issues, "baseline_version: is required")
	}
	if caseFile.ScenarioType == "fault" && strings.TrimSpace(caseFile.FaultVersion) == "" {
		issues = append(issues, "fault_version: is required for fault cases")
	}
	if caseFile.Window.Start.IsZero() {
		issues = append(issues, "window.start: is required")
	}
	if caseFile.Window.End.IsZero() {
		issues = append(issues, "window.end: is required")
	} else if !caseFile.Window.Start.IsZero() && caseFile.Window.End.Before(caseFile.Window.Start) {
		issues = append(issues, "window.end: must not be before window.start")
	}
	if caseFile.Repetitions < 1 {
		issues = append(issues, "repetitions: must be positive")
	}
	if strings.TrimSpace(caseFile.ExpectedRootCause.Category) == "" {
		issues = append(issues, "expected_root_cause.category: is required")
	}
	if strings.TrimSpace(caseFile.ExpectedRootCause.Summary) == "" {
		issues = append(issues, "expected_root_cause.summary: is required")
	}
	if len(caseFile.AllowedVariants) == 0 {
		issues = append(issues, "allowed_variants: at least one candidate is required")
	}
	for index, variant := range caseFile.AllowedVariants {
		if strings.TrimSpace(variant) == "" {
			issues = append(issues, fmt.Sprintf("allowed_variants[%d]: must not be empty", index))
		}
	}
	if len(caseFile.RequiredEvidence) == 0 {
		issues = append(issues, "required_evidence: at least one predicate is required")
	}
	for index, evidence := range caseFile.RequiredEvidence {
		if !supportedEvidenceSource(evidence.Source) {
			issues = append(issues, fmt.Sprintf("required_evidence[%d].source: unsupported value", index))
		}
		if strings.TrimSpace(evidence.Predicate) == "" {
			issues = append(issues, fmt.Sprintf("required_evidence[%d].predicate: is required", index))
		}
		if strings.TrimSpace(evidence.TimeRelation) == "" {
			issues = append(issues, fmt.Sprintf("required_evidence[%d].time_relation: is required", index))
		}
	}
	if len(caseFile.Counterexamples) == 0 {
		issues = append(issues, "counterexamples: at least one counterexample is required")
	}
	if len(caseFile.ForbiddenConclusions) == 0 {
		issues = append(issues, "forbidden_conclusions: at least one forbidden conclusion is required")
	}
	if !isJSONObject(caseFile.HealthyBaseline) {
		issues = append(issues, "healthy_baseline: must be a JSON object")
	}
	if len(caseFile.Injection.Steps) == 0 {
		issues = append(issues, "injection.steps: at least one step is required")
	}
	if len(caseFile.Recovery.Steps) == 0 {
		issues = append(issues, "recovery.steps: at least one step is required")
	}
	if len(caseFile.CleanupRequirements) == 0 {
		issues = append(issues, "cleanup_requirements: at least one requirement is required")
	}
	if len(caseFile.ExpectedTelemetry) == 0 {
		issues = append(issues, "expected_telemetry: at least one signal is required")
	}
	if len(caseFile.ExpectedChanges) == 0 {
		issues = append(issues, "expected_changes: at least one expected change is required")
	}
	if strings.TrimSpace(caseFile.Scoring.Version) == "" {
		issues = append(issues, "scoring.version: is required")
	}
	if len(caseFile.Scoring.Metrics) == 0 {
		issues = append(issues, "scoring.metrics: at least one metric is required")
	}
	if len(issues) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidCaseSet, strings.Join(issues, "; "))
	}
	return nil
}

func ValidateCaseSet(cases []Case) error {
	versions := make(map[string]struct{}, len(cases))
	for index, caseFile := range cases {
		if err := caseFile.Validate(); err != nil {
			return fmt.Errorf("case[%d]: %w", index, err)
		}
		key := fmt.Sprintf("%s@%d", caseFile.CaseID, caseFile.Version)
		if _, exists := versions[key]; exists {
			return fmt.Errorf("%w: %s", ErrDuplicateVersion, key)
		}
		versions[key] = struct{}{}
	}
	return nil
}

func supportedEvidenceSource(source string) bool {
	switch source {
	case "DEPLOYMENT", "GITHUB", "PROMETHEUS", "LOKI", "RUNTIME", "MAPPING", "SYSTEM":
		return true
	default:
		return false
	}
}

func isJSONObject(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}
