package domain_test

import (
	"errors"
	"testing"

	"github.com/Weiki886/relythra/internal/domain"
)

func TestDeploymentAcceptsDocumentedTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from domain.DeploymentStatus
		to   domain.DeploymentStatus
	}{
		{from: domain.DeploymentPending, to: domain.DeploymentInProgress},
		{from: domain.DeploymentPending, to: domain.DeploymentSucceeded},
		{from: domain.DeploymentPending, to: domain.DeploymentFailed},
		{from: domain.DeploymentPending, to: domain.DeploymentUnknown},
		{from: domain.DeploymentInProgress, to: domain.DeploymentSucceeded},
		{from: domain.DeploymentInProgress, to: domain.DeploymentFailed},
		{from: domain.DeploymentInProgress, to: domain.DeploymentUnknown},
		{from: domain.DeploymentUnknown, to: domain.DeploymentInProgress},
		{from: domain.DeploymentUnknown, to: domain.DeploymentSucceeded},
		{from: domain.DeploymentUnknown, to: domain.DeploymentFailed},
		{from: domain.DeploymentSucceeded, to: domain.DeploymentRolledBack},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(string(testCase.from)+"->"+string(testCase.to), func(t *testing.T) {
			t.Parallel()
			event := domain.DeploymentEvent{Status: testCase.from}
			if err := event.Transition(testCase.to); err != nil {
				t.Fatalf("Transition(%q) error = %v", testCase.to, err)
			}
			if event.Status != testCase.to {
				t.Fatalf("status = %q, want %q", event.Status, testCase.to)
			}
		})
	}
}

func TestDeploymentRejectsInvalidAndTerminalTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from domain.DeploymentStatus
		to   domain.DeploymentStatus
	}{
		{from: "", to: domain.DeploymentPending},
		{from: domain.DeploymentPending, to: domain.DeploymentPending},
		{from: domain.DeploymentFailed, to: domain.DeploymentInProgress},
		{from: domain.DeploymentRolledBack, to: domain.DeploymentSucceeded},
		{from: domain.DeploymentUnknown, to: domain.DeploymentRolledBack},
		{from: domain.DeploymentSucceeded, to: domain.DeploymentFailed},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(string(testCase.from)+"->"+string(testCase.to), func(t *testing.T) {
			t.Parallel()
			event := domain.DeploymentEvent{Status: testCase.from}
			err := event.Transition(testCase.to)
			if !errors.Is(err, domain.ErrInvalidDeploymentTransition) {
				t.Fatalf("Transition error = %v, want ErrInvalidDeploymentTransition", err)
			}
			if event.Status != testCase.from {
				t.Fatalf("rejected transition changed status to %q", event.Status)
			}
		})
	}
	if domain.DeploymentStatus("NOT_A_STATUS").Valid() {
		t.Fatal("unknown deployment status is valid")
	}
}
