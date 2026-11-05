package datasource

import "testing"

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
		Service: "checkout-api",
		Start:   "2026-11-04T13:00:00+08:00",
		End:     "2026-11-04T06:30:00Z",
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}
	if query.Limit != 20 || query.MaxSeries != 100 {
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
