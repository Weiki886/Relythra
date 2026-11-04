package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const canarySecret = "relythra-canary-secret-9f2b"

func TestResolverResolvesEnvironmentAndFileReferences(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(
		func(name string) (string, bool) {
			if name == "RELYTHRA_GITHUB_TOKEN" {
				return canarySecret, true
			}
			return "", false
		},
		func(name string) ([]byte, error) {
			if name != "/run/secrets/relythra-token" {
				return nil, errors.New("unexpected file")
			}
			return []byte(canarySecret + "\r\n"), nil
		},
	)

	for _, raw := range []string{
		"env:RELYTHRA_GITHUB_TOKEN",
		"file:/run/secrets/relythra-token",
	} {
		reference, err := ParseReference(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		secret, err := resolver.Resolve(context.Background(), reference)
		if err != nil {
			t.Fatalf("resolve %q: %v", raw, err)
		}
		if secret.Reveal() != canarySecret {
			t.Fatalf("resolved secret = %q, want canary", secret.Reveal())
		}
	}
}

func TestResolverRejectsMissingInvalidAndOversizedReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		lookup   func(string) (string, bool)
		readFile func(string) ([]byte, error)
	}{
		{
			name:   "missing reference",
			raw:    "",
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name:   "unsupported scheme",
			raw:    "vault:github-token",
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name:   "relative secret file",
			raw:    "file:relative-token",
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name:   "missing environment value",
			raw:    "env:RELYTHRA_MISSING",
			lookup: func(string) (string, bool) { return "", false },
		},
		{
			name:   "empty environment value",
			raw:    "env:RELYTHRA_EMPTY",
			lookup: func(string) (string, bool) { return "", true },
		},
		{
			name: "oversized file",
			raw:  "file:/run/secrets/large",
			lookup: func(string) (string, bool) {
				return "", false
			},
			readFile: func(string) ([]byte, error) {
				return []byte(strings.Repeat("x", MaxSecretBytes+1)), nil
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			lookup := testCase.lookup
			if lookup == nil {
				lookup = func(string) (string, bool) { return "", false }
			}
			readFile := testCase.readFile
			if readFile == nil {
				readFile = func(string) ([]byte, error) { return nil, errors.New("not found") }
			}
			resolver := NewResolver(lookup, readFile)

			reference, err := ParseReference(testCase.raw)
			if err == nil {
				_, err = resolver.Resolve(context.Background(), reference)
			}
			if err == nil {
				t.Fatal("invalid or unavailable reference was accepted")
			}
			if strings.Contains(err.Error(), canarySecret) {
				t.Fatal("resolver error exposed secret value")
			}
		})
	}
}

func TestSecretValueCannotLeakThroughDefaultFormatting(t *testing.T) {
	t.Parallel()

	secret := Secret{value: canarySecret}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	formatted := fmt.Sprintf("%s %v %+v %#v", secret, secret, secret, secret)
	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshal secret: %v", err)
	}
	logger.Info("secret fixture", "value", secret)

	for label, output := range map[string]string{
		"fmt":  formatted,
		"json": string(encoded),
		"log":  logs.String(),
	} {
		if strings.Contains(output, canarySecret) {
			t.Errorf("%s output exposed secret: %s", label, output)
		}
		if !strings.Contains(output, "[REDACTED]") {
			t.Errorf("%s output has no redaction marker: %s", label, output)
		}
	}
}
