package domain_test

import (
	"errors"
	"testing"

	"github.com/Weiki886/relythra/internal/domain"
)

func TestInvestigationAcceptsDocumentedTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from domain.InvestigationStatus
		to   domain.InvestigationStatus
	}{
		{from: domain.InvestigationReceived, to: domain.InvestigationMatched},
		{from: domain.InvestigationMatched, to: domain.InvestigationCollecting},
		{from: domain.InvestigationCollecting, to: domain.InvestigationHypothesizing},
		{from: domain.InvestigationHypothesizing, to: domain.InvestigationVerifying},
		{from: domain.InvestigationVerifying, to: domain.InvestigationCompleted},
		{from: domain.InvestigationVerifying, to: domain.InvestigationInsufficientEvidence},
		{from: domain.InvestigationVerifying, to: domain.InvestigationFailed},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.from)+"->"+string(test.to), func(t *testing.T) {
			t.Parallel()

			investigation := domain.Investigation{Status: test.from}
			if err := investigation.Transition(test.to); err != nil {
				t.Fatalf("Transition(%q) error = %v", test.to, err)
			}
			if investigation.Status != test.to {
				t.Errorf("Status = %q, want %q", investigation.Status, test.to)
			}
		})
	}
}

func TestInvestigationRejectsUndocumentedOrRepeatedTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from domain.InvestigationStatus
		to   domain.InvestigationStatus
	}{
		{from: "", to: domain.InvestigationReceived},
		{from: domain.InvestigationReceived, to: domain.InvestigationCollecting},
		{from: domain.InvestigationMatched, to: domain.InvestigationReceived},
		{from: domain.InvestigationCollecting, to: domain.InvestigationFailed},
		{from: domain.InvestigationVerifying, to: domain.InvestigationVerifying},
		{from: domain.InvestigationCompleted, to: domain.InvestigationVerifying},
		{from: domain.InvestigationInsufficientEvidence, to: domain.InvestigationCompleted},
		{from: domain.InvestigationFailed, to: domain.InvestigationReceived},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.from)+"->"+string(test.to), func(t *testing.T) {
			t.Parallel()

			investigation := domain.Investigation{Status: test.from}
			err := investigation.Transition(test.to)
			if !errors.Is(err, domain.ErrInvalidTransition) {
				t.Fatalf("Transition() error = %v, want ErrInvalidTransition", err)
			}
			if investigation.Status != test.from {
				t.Errorf("Status changed to %q after rejected transition, want %q", investigation.Status, test.from)
			}
		})
	}
}

func TestOtherStatusesRejectUnknownValues(t *testing.T) {
	t.Parallel()

	if domain.DeploymentMatchStatus("unknown").Valid() {
		t.Error("unknown deployment match status is valid")
	}
	if domain.IncidentStatus("unknown").Valid() {
		t.Error("unknown incident status is valid")
	}
	if domain.NotificationStatus("unknown").Valid() {
		t.Error("unknown notification status is valid")
	}
}
