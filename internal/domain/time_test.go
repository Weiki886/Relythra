package domain_test

import (
	"testing"
	"time"

	"github.com/Weiki886/relythra/internal/domain"
)

func TestNormalizeTimeConvertsToUTC(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("UTC+08", 8*60*60)
	value := time.Date(2026, time.November, 4, 12, 30, 0, 0, location)
	got := domain.NormalizeTime(value)

	if got.Location() != time.UTC {
		t.Fatalf("Location = %v, want UTC", got.Location())
	}
	if !got.Equal(value) {
		t.Fatalf("NormalizeTime() = %v, want same instant as %v", got, value)
	}
}

func TestValidateTimeRange(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.November, 4, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		start   time.Time
		end     time.Time
		wantErr bool
	}{
		{name: "valid range", start: start, end: start.Add(time.Minute)},
		{name: "same instant", start: start, end: start},
		{name: "missing start", end: start, wantErr: true},
		{name: "inverted", start: start, end: start.Add(-time.Second), wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := domain.ValidateTimeRange(test.start, test.end)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateTimeRange() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
