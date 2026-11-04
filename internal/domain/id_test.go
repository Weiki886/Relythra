package domain_test

import (
	"testing"

	"github.com/Weiki886/relythra/internal/domain"
)

func TestNewIDProducesCanonicalV4(t *testing.T) {
	t.Parallel()

	first, err := domain.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	second, err := domain.NewID()
	if err != nil {
		t.Fatalf("second NewID() error = %v", err)
	}

	if !domain.IsID(first) {
		t.Fatalf("NewID() = %q, want canonical UUID", first)
	}
	if first == second {
		t.Fatalf("NewID() returned duplicate value %q", first)
	}
	if first[14] != '4' {
		t.Errorf("UUID version nibble = %q, want 4", first[14])
	}
	if first[19] != '8' && first[19] != '9' && first[19] != 'a' && first[19] != 'b' {
		t.Errorf("UUID variant nibble = %q, want RFC 4122 variant", first[19])
	}
}

func TestIsIDRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"not-a-uuid",
		"00000000-0000-1000-8000-000000000000",
		"00000000-0000-4000-7000-000000000000",
		"00000000000040008000000000000000",
	} {
		if domain.IsID(value) {
			t.Errorf("IsID(%q) = true, want false", value)
		}
	}
}
