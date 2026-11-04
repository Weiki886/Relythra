// Package secrets resolves narrowly scoped runtime credentials from explicit
// environment variable or file references.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// MaxSecretBytes bounds accidental reads of large or malformed files.
	MaxSecretBytes = 64 * 1024
	redactedValue  = "[REDACTED]"
)

var (
	ErrInvalidReference = errors.New("invalid secret reference")
	ErrUnavailable      = errors.New("secret reference is unavailable")
	ErrEmptySecret      = errors.New("resolved secret is empty")
	ErrSecretTooLarge   = errors.New("secret exceeds size limit")

	environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Reference is a non-secret pointer to runtime credential material.
type Reference struct {
	scheme  string
	locator string
}

// ParseReference accepts only explicit env:NAME and absolute file:/path forms.
func ParseReference(raw string) (Reference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Reference{}, fmt.Errorf("%w: reference is required", ErrInvalidReference)
	}
	if strings.ContainsRune(raw, 0) {
		return Reference{}, fmt.Errorf("%w: reference contains a null byte", ErrInvalidReference)
	}

	scheme, locator, ok := strings.Cut(raw, ":")
	if !ok || locator == "" {
		return Reference{}, fmt.Errorf("%w: expected env:NAME or file:/path", ErrInvalidReference)
	}
	switch scheme {
	case "env":
		if !environmentName.MatchString(locator) {
			return Reference{}, fmt.Errorf("%w: invalid environment variable name", ErrInvalidReference)
		}
	case "file":
		if !filepath.IsAbs(locator) {
			return Reference{}, fmt.Errorf("%w: secret file path must be absolute", ErrInvalidReference)
		}
	default:
		return Reference{}, fmt.Errorf("%w: unsupported reference scheme", ErrInvalidReference)
	}
	return Reference{scheme: scheme, locator: locator}, nil
}

func (reference Reference) String() string {
	if reference.scheme == "" {
		return ""
	}
	return reference.scheme + ":" + reference.locator
}

// Secret keeps credential material behind an explicit Reveal call. Default
// formatting, JSON encoding, and slog all use a fixed redaction marker.
type Secret struct {
	value string
}

// New wraps an already loaded credential after enforcing the same bounds as
// reference resolution. Callers should prefer Resolve for new configuration.
func New(value string) (Secret, error) {
	if len(value) > MaxSecretBytes {
		return Secret{}, ErrSecretTooLarge
	}
	if strings.TrimSpace(value) == "" {
		return Secret{}, ErrEmptySecret
	}
	return Secret{value: value}, nil
}

func (secret Secret) Reveal() string {
	return secret.value
}

func (Secret) String() string {
	return redactedValue
}

func (Secret) GoString() string {
	return redactedValue
}

func (Secret) LogValue() slog.Value {
	return slog.StringValue(redactedValue)
}

func (Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedValue)
}

type lookupEnvironment func(string) (string, bool)
type readSecretFile func(string) ([]byte, error)

// Resolver reads references on demand and never caches their values.
type Resolver struct {
	lookupEnvironment lookupEnvironment
	readSecretFile    readSecretFile
	maxBytes          int
}

func NewResolver(lookup lookupEnvironment, readFile readSecretFile) *Resolver {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if readFile == nil {
		readFile = func(path string) ([]byte, error) {
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			return io.ReadAll(io.LimitReader(file, MaxSecretBytes+1))
		}
	}
	return &Resolver{
		lookupEnvironment: lookup,
		readSecretFile:    readFile,
		maxBytes:          MaxSecretBytes,
	}
}

func (resolver *Resolver) Resolve(ctx context.Context, reference Reference) (Secret, error) {
	if resolver == nil {
		return Secret{}, fmt.Errorf("%w: resolver is not configured", ErrUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Secret{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	var value string
	switch reference.scheme {
	case "env":
		resolved, ok := resolver.lookupEnvironment(reference.locator)
		if !ok {
			return Secret{}, fmt.Errorf("%w: environment variable is not set", ErrUnavailable)
		}
		value = resolved
	case "file":
		resolved, err := resolver.readSecretFile(reference.locator)
		if err != nil {
			return Secret{}, fmt.Errorf("%w: secret file could not be read", ErrUnavailable)
		}
		if len(resolved) > resolver.maxBytes {
			return Secret{}, ErrSecretTooLarge
		}
		value = trimSingleLineEnding(string(resolved))
	default:
		return Secret{}, ErrInvalidReference
	}

	return New(value)
}

func trimSingleLineEnding(value string) string {
	switch {
	case strings.HasSuffix(value, "\r\n"):
		return strings.TrimSuffix(value, "\r\n")
	case strings.HasSuffix(value, "\n"):
		return strings.TrimSuffix(value, "\n")
	default:
		return value
	}
}
