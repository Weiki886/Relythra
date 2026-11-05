package diagnosiscase

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestDecodeValidSlowSQLCase(t *testing.T) {
	caseFile, err := Decode([]byte(validCaseJSON))
	if err != nil {
		t.Fatalf("Decode valid case: %v", err)
	}
	if caseFile.CaseID != "slow-sql-checkout-v1" || caseFile.Version != 1 {
		t.Fatalf("identity = %q/%d", caseFile.CaseID, caseFile.Version)
	}
	if caseFile.Window.Start.After(caseFile.Window.End) {
		t.Fatalf("invalid decoded window: %+v", caseFile.Window)
	}
}

func TestDecodeRejectsMalformedAndIncompleteCases(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "missing required field", data: `{"case_id":"x"}`, want: "version"},
		{name: "unknown field", data: strings.Replace(validCaseJSON, `"case_id":`, `"unexpected_debug":true,"case_id":`, 1), want: "unexpected_debug"},
		{name: "trailing document", data: validCaseJSON + `{}`, want: "trailing"},
		{name: "inverted time window", data: strings.Replace(validCaseJSON, "2026-11-05T10:00:00Z", "2026-11-05T09:00:00Z", 1), want: "window.end"},
		{name: "empty candidate set", data: strings.Replace(validCaseJSON, `"allowed_variants": ["query-plan-regression"]`, `"allowed_variants": []`, 1), want: "allowed_variants"},
		{name: "unassessable evidence", data: strings.Replace(validCaseJSON, `"predicate": "orders P95 increased"`, `"predicate": " "`, 1), want: "predicate"},
		{name: "unsupported scenario", data: strings.Replace(validCaseJSON, `"scenario_type": "fault"`, `"scenario_type": "arbitrary"`, 1), want: "scenario_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode error = %v, want path containing %q", err, test.want)
			}
		})
	}
}

func TestCaseSetRejectsDuplicateVersionAndAcceptsNewVersion(t *testing.T) {
	caseV1, err := Decode([]byte(validCaseJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCaseSet([]Case{caseV1, caseV1}); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("duplicate version error = %v", err)
	}
	caseV2 := caseV1
	caseV2.Version = 2
	if err := ValidateCaseSet([]Case{caseV1, caseV2}); err != nil {
		t.Fatalf("distinct versions: %v", err)
	}
}

func TestRuntimeContextUsesPositiveAllowlist(t *testing.T) {
	caseFile, err := Decode([]byte(validCaseJSON))
	if err != nil {
		t.Fatal(err)
	}
	context := caseFile.RuntimeContext()
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, allowed := range []string{`"service":"checkout-api"`, `"environment":"demo"`, `"alert_started_at":"2026-11-05T09:30:00Z"`} {
		if !strings.Contains(text, allowed) {
			t.Errorf("runtime context omitted %s: %s", allowed, text)
		}
	}
	for _, forbidden := range []string{"canary-root-cause", "canary-injection", "canary-scenario", "canary-scoring", "case_id", "expected_root_cause", "injection", "scenario_metadata", "scoring"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("runtime context exposed %q: %s", forbidden, text)
		}
	}
}

func TestRuntimeCannotReadEvaluationCaseAndDenialIsAudited(t *testing.T) {
	filesystem := fstest.MapFS{
		"slow-sql/v1.json": &fstest.MapFile{Data: []byte(validCaseJSON)},
	}
	var events []AccessDenied
	caseFile, err := Read(filesystem, "slow-sql/v1.json", Runtime, func(event AccessDenied) {
		events = append(events, event)
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("runtime read error = %v, want ErrAccessDenied", err)
	}
	if caseFile.CaseID != "" {
		t.Fatalf("runtime received case content: %+v", caseFile)
	}
	if len(events) != 1 || events[0].Principal != Runtime || events[0].Path != "slow-sql/v1.json" {
		t.Fatalf("denial audit events = %+v", events)
	}
}

func TestEvaluationReadValidatesCaseAndRuntimeDenialFailsClosed(t *testing.T) {
	filesystem := fstest.MapFS{
		"slow-sql/v1.json": &fstest.MapFile{Data: []byte(validCaseJSON)},
	}
	caseFile, err := Read(filesystem, "slow-sql/v1.json", Evaluator, func(AccessDenied) {})
	if err != nil || caseFile.CaseID != "slow-sql-checkout-v1" {
		t.Fatalf("evaluator read = %+v, %v", caseFile, err)
	}
	_, err = Read(filesystem, "slow-sql/v1.json", Runtime, nil)
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("unauditable runtime denial = %v, want ErrAuditUnavailable", err)
	}
}

func TestReadRejectsPathTraversalBeforeOpeningFile(t *testing.T) {
	filesystem := fstest.MapFS{"../case.json": &fstest.MapFile{Data: []byte(validCaseJSON)}}
	_, err := Read(filesystem, "../case.json", Evaluator, func(AccessDenied) {})
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal error = %v, want ErrInvalidPath", err)
	}
}

const validCaseJSON = `{
  "case_id": "slow-sql-checkout-v1",
  "version": 1,
  "scenario_type": "fault",
  "service": "checkout-api",
  "environment": "demo",
  "baseline_version": "release-100",
  "fault_version": "release-101",
  "window": {"start": "2026-11-05T09:30:00Z", "end": "2026-11-05T10:00:00Z"},
  "repetitions": 10,
  "randomize_order": true,
  "expected_root_cause": {"category": "database.query-plan-regression", "summary": "canary-root-cause"},
  "allowed_variants": ["query-plan-regression"],
  "required_evidence": [{"source": "PROMETHEUS", "predicate": "orders P95 increased", "time_relation": "after_fault_start"}],
  "counterexamples": ["deployment completed before the observed latency change"],
  "forbidden_conclusions": ["infrastructure failure without infrastructure evidence"],
  "healthy_baseline": {"p95_ms": 80},
  "injection": {"steps": ["run fixed slow query"], "state_marker": "canary-injection"},
  "recovery": {"steps": ["restore baseline query"], "cleanup": ["remove generated rows"]},
  "expected_telemetry": ["orders latency histogram rises"],
  "expected_changes": ["query code changes"],
  "cleanup_requirements": ["restore service to baseline"],
  "scoring": {"version": "canary-scoring", "metrics": ["top1", "top3", "evidence_completeness", "inconclusive_accuracy"]}
}`
