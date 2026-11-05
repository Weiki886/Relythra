// Package datasource defines the validated configuration contract for the
// external systems that Relythra may query during diagnosis.
package datasource

import (
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Weiki886/relythra/internal/security/secrets"
)

type SourceType string

const (
	SourceGitHub     SourceType = "GITHUB"
	SourceGrafana    SourceType = "GRAFANA"
	SourcePrometheus SourceType = "PROMETHEUS"
	SourceLoki       SourceType = "LOKI"
	SourceRuntime    SourceType = "RUNTIME"
)

func (source SourceType) Valid() bool {
	switch source {
	case SourceGitHub, SourceGrafana, SourcePrometheus, SourceLoki, SourceRuntime:
		return true
	default:
		return false
	}
}

type RuntimeType string

const (
	RuntimeDockerCompose RuntimeType = "docker-compose"
	RuntimeKubernetes    RuntimeType = "kubernetes"
)

func (runtime RuntimeType) Valid() bool {
	return runtime == RuntimeDockerCompose || runtime == RuntimeKubernetes
}

type ValidationIssue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type PublicSettings struct {
	Endpoint      string            `json:"endpoint"`
	Repository    string            `json:"repository,omitempty"`
	RuntimeLabels map[string]string `json:"runtime_labels,omitempty"`
}

var (
	repositoryPattern = regexp.MustCompile(
		`^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,99})/[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,99})$`,
	)
	composeLabels = []string{"project", "service", "env", "host_id", "container_id"}
)

func ValidateMapping(
	source SourceType,
	runtime RuntimeType,
	settings PublicSettings,
	templateIDs []string,
	secretReference *secrets.Reference,
) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if !source.Valid() {
		issues = append(issues, issue("source_type", "unsupported", "source type is not supported"))
	}
	if source == SourceRuntime {
		if runtime == "" {
			issues = append(issues, issue("runtime_type", "required", "runtime type is required"))
		} else if !runtime.Valid() {
			issues = append(issues, issue("runtime_type", "unsupported", "runtime type is not supported"))
		} else {
			issues = append(issues, ValidateRuntimeLabels(runtime, settings.RuntimeLabels)...)
		}
	} else if runtime != "" {
		issues = append(issues, issue("runtime_type", "not_applicable", "runtime type only applies to runtime mappings"))
	}

	issues = append(issues, validateEndpoint(settings.Endpoint)...)
	if source == SourceGitHub {
		repository := strings.TrimSpace(settings.Repository)
		switch {
		case repository == "":
			issues = append(issues, issue("public_settings.repository", "required", "GitHub repository is required"))
		case !repositoryPattern.MatchString(repository):
			issues = append(issues, issue("public_settings.repository", "invalid_format", "GitHub repository must use owner/name format"))
		}
	} else if strings.TrimSpace(settings.Repository) != "" {
		issues = append(issues, issue("public_settings.repository", "not_applicable", "repository only applies to GitHub mappings"))
	}

	if secretReference != nil {
		if _, err := secrets.ParseReference(secretReference.String()); err != nil {
			issues = append(issues, issue("secret_ref", "invalid_reference", "secret reference is invalid"))
		}
	}
	if len(templateIDs) == 0 {
		issues = append(issues, issue("template_ids", "required", "at least one query template is required"))
	} else {
		_, templateIssues := ResolveTemplates(source, templateIDs)
		issues = append(issues, templateIssues...)
	}
	return issues
}

func ValidateRuntimeLabels(runtime RuntimeType, labels map[string]string) []ValidationIssue {
	switch runtime {
	case RuntimeDockerCompose:
		issues := make([]ValidationIssue, 0)
		allowed := make(map[string]struct{}, len(composeLabels))
		for _, label := range composeLabels {
			allowed[label] = struct{}{}
			if strings.TrimSpace(labels[label]) == "" {
				issues = append(issues, issue(
					"runtime_labels."+label,
					"required",
					"required Compose label is missing",
				))
			}
		}
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, ok := allowed[key]; !ok {
				issues = append(issues, issue(
					"runtime_labels."+key,
					"unsupported_label",
					"label is not supported for Docker Compose",
				))
			}
		}
		return issues
	case RuntimeKubernetes:
		return []ValidationIssue{issue(
			"runtime_type",
			"unsupported_runtime",
			"Kubernetes runtime diagnosis is not supported in v1",
		)}
	default:
		return []ValidationIssue{issue(
			"runtime_type",
			"unsupported",
			"runtime type is not supported",
		)}
	}
}

func validateEndpoint(raw string) []ValidationIssue {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"required",
			"data source endpoint is required",
		)}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"invalid_url",
			"data source endpoint is invalid",
		)}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"unsupported_scheme",
			"data source endpoint must use HTTP or HTTPS",
		)}
	}
	if parsed.User != nil {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"credentials_not_allowed",
			"data source endpoint must not contain credentials",
		)}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.EscapedPath() != "" && parsed.EscapedPath() != "/") {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"base_url_required",
			"data source endpoint must be a base URL without query, fragment, or path",
		)}
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return []ValidationIssue{issue(
				"public_settings.endpoint",
				"invalid_port",
				"data source endpoint port is invalid",
			)}
		}
	}
	host := parsed.Hostname()
	if strings.ContainsAny(host, " /\\@") || net.ParseIP(host) != nil && strings.Contains(host, "%") {
		return []ValidationIssue{issue(
			"public_settings.endpoint",
			"invalid_host",
			"data source endpoint host is invalid",
		)}
	}
	return nil
}

func issue(field, code, message string) ValidationIssue {
	return ValidationIssue{Field: field, Code: code, Message: message}
}
