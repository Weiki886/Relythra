package datasource

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultQueryLimit     = 20
	MaxQueryLimit         = 100
	DefaultQueryMaxSeries = 100
	MaxQueryMaxSeries     = 1000
	MaxQueryWindow        = 6 * time.Hour
)

type Template struct {
	ID                string
	Version           int
	Source            SourceType
	Purpose           string
	ResultSchema      string
	AllowedParameters []string
}

type QueryParameters struct {
	Service   string
	Start     string
	End       string
	Limit     int
	Cursor    string
	MaxSeries int
	Extra     map[string]any
}

type NormalizedQuery struct {
	TemplateID string
	Version    int
	Service    string
	Start      time.Time
	End        time.Time
	Limit      int
	Cursor     string
	MaxSeries  int
}

var commonParameters = []string{
	"cursor",
	"end",
	"limit",
	"max_series",
	"service",
	"start",
}

var templateRegistry = map[string]Template{
	"github.commits.v1": {
		ID: "github.commits.v1", Version: 1, Source: SourceGitHub,
		Purpose:      "Read commits in the bounded diagnosis window.",
		ResultSchema: "code.commit_list.v1", AllowedParameters: commonParameters,
	},
	"github.pull_requests.v1": {
		ID: "github.pull_requests.v1", Version: 1, Source: SourceGitHub,
		Purpose:      "Read pull requests associated with release commits.",
		ResultSchema: "code.pull_request_list.v1", AllowedParameters: commonParameters,
	},
	"github.diff.v1": {
		ID: "github.diff.v1", Version: 1, Source: SourceGitHub,
		Purpose:      "Read a bounded pull request or commit diff.",
		ResultSchema: "code.diff.v1", AllowedParameters: commonParameters,
	},
	"grafana.alert.v1": {
		ID: "grafana.alert.v1", Version: 1, Source: SourceGrafana,
		Purpose:      "Read the alert definition associated with a fingerprint.",
		ResultSchema: "alert.grafana.v1", AllowedParameters: commonParameters,
	},
	"grafana.health.v1": {
		ID: "grafana.health.v1", Version: 1, Source: SourceGrafana,
		Purpose:      "Read datasource health and version metadata.",
		ResultSchema: "source.health.v1", AllowedParameters: commonParameters,
	},
	"prometheus.service.latency.v1": {
		ID: "prometheus.service.latency.v1", Version: 1, Source: SourcePrometheus,
		Purpose:      "Read bounded service latency series.",
		ResultSchema: "metric.series.v1", AllowedParameters: commonParameters,
	},
	"prometheus.container.memory.v1": {
		ID: "prometheus.container.memory.v1", Version: 1, Source: SourcePrometheus,
		Purpose:      "Read bounded container memory series.",
		ResultSchema: "metric.series.v1", AllowedParameters: commonParameters,
	},
	"prometheus.node.capacity.v1": {
		ID: "prometheus.node.capacity.v1", Version: 1, Source: SourcePrometheus,
		Purpose:      "Read bounded host capacity series.",
		ResultSchema: "metric.series.v1", AllowedParameters: commonParameters,
	},
	"loki.service.errors.v1": {
		ID: "loki.service.errors.v1", Version: 1, Source: SourceLoki,
		Purpose:      "Read bounded error log entries.",
		ResultSchema: "log.entry_list.v1", AllowedParameters: commonParameters,
	},
	"loki.container.restarts.v1": {
		ID: "loki.container.restarts.v1", Version: 1, Source: SourceLoki,
		Purpose:      "Read bounded container restart log entries.",
		ResultSchema: "log.entry_list.v1", AllowedParameters: commonParameters,
	},
	"runtime.containers.v1": {
		ID: "runtime.containers.v1", Version: 1, Source: SourceRuntime,
		Purpose:      "Read Docker container state through fixed metrics.",
		ResultSchema: "runtime.container_list.v1", AllowedParameters: commonParameters,
	},
	"runtime.host.v1": {
		ID: "runtime.host.v1", Version: 1, Source: SourceRuntime,
		Purpose:      "Read host capacity metrics.",
		ResultSchema: "runtime.host_state.v1", AllowedParameters: commonParameters,
	},
	"runtime.events.v1": {
		ID: "runtime.events.v1", Version: 1, Source: SourceRuntime,
		Purpose:      "Read bounded container lifecycle events.",
		ResultSchema: "runtime.event_list.v1", AllowedParameters: commonParameters,
	},
}

var servicePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func ResolveTemplates(source SourceType, templateIDs []string) ([]Template, []ValidationIssue) {
	templates := make([]Template, 0, len(templateIDs))
	issues := make([]ValidationIssue, 0)
	seen := make(map[string]struct{}, len(templateIDs))
	for _, rawID := range templateIDs {
		id := strings.TrimSpace(rawID)
		template, ok := templateRegistry[id]
		if !ok || template.Source != source {
			issues = append(issues, issue(
				"template_ids",
				"unsupported_template",
				fmt.Sprintf("query template %q is not available for this source", id),
			))
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			issues = append(issues, issue(
				"template_ids",
				"duplicate_template",
				fmt.Sprintf("query template %q is duplicated", id),
			))
			continue
		}
		seen[id] = struct{}{}
		templates = append(templates, template)
	}
	return templates, issues
}

func ValidateQuery(template Template, parameters QueryParameters) (NormalizedQuery, []ValidationIssue) {
	issues := make([]ValidationIssue, 0)
	allowed := make(map[string]struct{}, len(template.AllowedParameters))
	for _, parameter := range template.AllowedParameters {
		allowed[parameter] = struct{}{}
	}
	extraKeys := make([]string, 0, len(parameters.Extra))
	for key := range parameters.Extra {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		if _, ok := allowed[key]; !ok {
			issues = append(issues, issue(
				"parameters."+key,
				"unsupported_parameter",
				"query parameter is not allowed by the fixed template",
			))
		}
	}

	service := strings.TrimSpace(parameters.Service)
	if !servicePattern.MatchString(service) {
		issues = append(issues, issue(
			"parameters.service",
			"invalid_format",
			"service must be a lowercase service slug",
		))
	}
	start, startOK := parseQueryTime(parameters.Start, "parameters.start", &issues)
	end, endOK := parseQueryTime(parameters.End, "parameters.end", &issues)
	if startOK && endOK {
		switch {
		case !end.After(start):
			issues = append(issues, issue(
				"parameters.end",
				"invalid_range",
				"query end must be after query start",
			))
		case end.Sub(start) > MaxQueryWindow:
			issues = append(issues, issue(
				"parameters.end",
				"window_too_large",
				"query window exceeds the fixed maximum",
			))
		}
	}

	limit := parameters.Limit
	if limit == 0 {
		limit = DefaultQueryLimit
	}
	if limit < 1 || limit > MaxQueryLimit {
		issues = append(issues, issue(
			"parameters.limit",
			"out_of_range",
			"query limit is outside the allowed range",
		))
	}
	maxSeries := parameters.MaxSeries
	if maxSeries == 0 {
		maxSeries = DefaultQueryMaxSeries
	}
	if maxSeries < 1 || maxSeries > MaxQueryMaxSeries {
		issues = append(issues, issue(
			"parameters.max_series",
			"out_of_range",
			"maximum series count is outside the allowed range",
		))
	}
	cursor := strings.TrimSpace(parameters.Cursor)
	if len(cursor) > 512 || strings.ContainsAny(cursor, "\x00\r\n") {
		issues = append(issues, issue(
			"parameters.cursor",
			"invalid_format",
			"query cursor is invalid",
		))
	}

	return NormalizedQuery{
		TemplateID: template.ID,
		Version:    template.Version,
		Service:    service,
		Start:      start.UTC(),
		End:        end.UTC(),
		Limit:      limit,
		Cursor:     cursor,
		MaxSeries:  maxSeries,
	}, issues
}

func parseQueryTime(raw, field string, issues *[]ValidationIssue) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		*issues = append(*issues, issue(field, "required", "query time is required"))
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		*issues = append(*issues, issue(field, "invalid_format", "query time must use RFC3339"))
		return time.Time{}, false
	}
	return parsed, true
}
