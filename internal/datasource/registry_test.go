package datasource

import (
	"testing"
	"time"
)

func TestResolveTemplatesRejectsTemplateFromAnotherSource(t *testing.T) {
	_, issues := ResolveTemplates(SourceLoki, []string{"prometheus.service.latency.v1"})
	if !hasIssue(issues, "template_ids", "unsupported_template") {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestResolveTemplatesReturnsServerMetadata(t *testing.T) {
	templates, issues := ResolveTemplates(
		SourcePrometheus,
		[]string{"prometheus.service.latency.v1"},
	)
	if len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}
	if len(templates) != 1 ||
		templates[0].Version != 1 ||
		templates[0].ResultSchema == "" {
		t.Fatalf("templates = %#v", templates)
	}
}

func TestValidateQueryRejectsUnknownQueryText(t *testing.T) {
	templates, issues := ResolveTemplates(
		SourcePrometheus,
		[]string{"prometheus.service.latency.v1"},
	)
	if len(issues) != 0 {
		t.Fatalf("resolve issues = %#v", issues)
	}
	_, issues = ValidateQuery(templates[0], QueryParameters{
		Service: "checkout-api",
		Extra:   map[string]any{"promql": "up"},
	})
	if !hasIssue(issues, "parameters.promql", "unsupported_parameter") {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateQueryAppliesDefaultsAndNormalizesUTC(t *testing.T) {
	templates, issues := ResolveTemplates(
		SourcePrometheus,
		[]string{"prometheus.service.latency.v1"},
	)
	if len(issues) != 0 {
		t.Fatalf("resolve issues = %#v", issues)
	}
	query, issues := ValidateQuery(templates[0], QueryParameters{
		Service: "checkout-api", Environment: "prod",
		Start: "2026-11-04T13:00:00+08:00", End: "2026-11-04T06:30:00Z",
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}
	if query.Limit != 20 || query.MaxSeries != DefaultQueryMaxSeries || query.MaxPoints != DefaultQueryMaxPoints || query.Step != DefaultQueryStep {
		t.Fatalf("query defaults = %#v", query)
	}
	if query.Start.Location().String() != "UTC" || query.End.Location().String() != "UTC" {
		t.Fatalf("query times are not UTC: %#v", query)
	}
}

func TestValidateQueryRejectsUnboundedWindow(t *testing.T) {
	templates, _ := ResolveTemplates(
		SourceLoki,
		[]string{"loki.service.errors.v1"},
	)
	_, issues := ValidateQuery(templates[0], QueryParameters{
		Service: "checkout-api",
		Start:   "2026-11-01T00:00:00Z",
		End:     "2026-11-04T00:00:00Z",
	})
	if !hasIssue(issues, "parameters.end", "window_too_large") {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateNonPrometheusQueryKeepsExistingBudgetShape(t *testing.T) {
	templates, issues := ResolveTemplates(SourceLoki, []string{"loki.service.errors.v1"})
	if len(issues) != 0 {
		t.Fatalf("resolve issues = %#v", issues)
	}
	query, issues := ValidateQuery(templates[0], QueryParameters{
		Service: "checkout-api", Start: "2026-11-04T05:00:00Z", End: "2026-11-04T06:00:00Z",
	})
	if len(issues) != 0 {
		t.Fatalf("validate issues = %#v", issues)
	}
	if query.MaxSeries != MaxQueryMaxSeries || query.Step != 0 || query.MaxPoints != 0 {
		t.Fatalf("non-Prometheus query includes Prometheus defaults: %#v", query)
	}
}

func TestValidatePrometheusQueryAppliesEnvironmentAndSampleBudgets(t *testing.T) {
	templates, issues := ResolveTemplates(SourcePrometheus, []string{"prometheus.service.latency.v1"})
	if len(issues) != 0 {
		t.Fatalf("resolve issues = %#v", issues)
	}
	query, issues := ValidateQuery(templates[0], QueryParameters{
		Service: "checkout-api", Environment: "prod",
		Start: "2026-11-04T05:00:00Z", End: "2026-11-04T06:00:00Z",
	})
	if len(issues) != 0 {
		t.Fatalf("validate issues = %#v", issues)
	}
	if query.Environment != "prod" || query.Step != 30*time.Second || query.MaxPoints != DefaultQueryMaxPoints {
		t.Fatalf("normalized Prometheus budget = %#v", query)
	}
}

func TestValidatePrometheusQueryRejectsEnvironmentAndPointBudgetBeforeExecution(t *testing.T) {
	templates, _ := ResolveTemplates(SourcePrometheus, []string{"prometheus.service.latency.v1"})
	base := QueryParameters{Service: "checkout-api", Environment: "prod", Start: "2026-11-04T00:00:00Z", End: "2026-11-04T01:00:00Z"}

	missingEnvironment := base
	missingEnvironment.Environment = ""
	_, issues := ValidateQuery(templates[0], missingEnvironment)
	if !hasIssue(issues, "parameters.environment", "required") {
		t.Fatalf("missing environment issues = %#v", issues)
	}

	tooFewPoints := base
	tooFewPoints.Step = "5s"
	tooFewPoints.MaxPoints = 100
	_, issues = ValidateQuery(templates[0], tooFewPoints)
	if !hasIssue(issues, "parameters.max_points", "insufficient_budget") {
		t.Fatalf("sample budget issues = %#v", issues)
	}

	tooManySamples := base
	tooManySamples.MaxSeries = 100
	tooManySamples.MaxPoints = 201
	_, issues = ValidateQuery(templates[0], tooManySamples)
	if !hasIssue(issues, "parameters.max_points", "total_sample_budget_exceeded") {
		t.Fatalf("total sample issues = %#v", issues)
	}
}
