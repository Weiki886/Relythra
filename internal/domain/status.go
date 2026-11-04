package domain

import (
	"errors"
	"fmt"
)

var ErrInvalidTransition = errors.New("invalid state transition")

type InvestigationStatus string

const (
	InvestigationReceived             InvestigationStatus = "RECEIVED"
	InvestigationMatched              InvestigationStatus = "MATCHED"
	InvestigationCollecting           InvestigationStatus = "COLLECTING"
	InvestigationHypothesizing        InvestigationStatus = "HYPOTHESIZING"
	InvestigationVerifying            InvestigationStatus = "VERIFYING"
	InvestigationCompleted            InvestigationStatus = "COMPLETED"
	InvestigationInsufficientEvidence InvestigationStatus = "INSUFFICIENT_EVIDENCE"
	InvestigationFailed               InvestigationStatus = "FAILED"
)

var investigationTransitions = map[InvestigationStatus]map[InvestigationStatus]struct{}{
	InvestigationReceived: {
		InvestigationMatched: {},
	},
	InvestigationMatched: {
		InvestigationCollecting: {},
	},
	InvestigationCollecting: {
		InvestigationHypothesizing: {},
	},
	InvestigationHypothesizing: {
		InvestigationVerifying: {},
	},
	InvestigationVerifying: {
		InvestigationCompleted:            {},
		InvestigationInsufficientEvidence: {},
		InvestigationFailed:               {},
	},
}

func (status InvestigationStatus) Valid() bool {
	_, ok := investigationTransitions[status]
	if ok {
		return true
	}
	return status == InvestigationCompleted ||
		status == InvestigationInsufficientEvidence ||
		status == InvestigationFailed
}

type TransitionError struct {
	From InvestigationStatus
	To   InvestigationStatus
}

func (err *TransitionError) Error() string {
	return fmt.Sprintf("cannot transition investigation from %q to %q", err.From, err.To)
}

func (err *TransitionError) Unwrap() error {
	return ErrInvalidTransition
}

type Investigation struct {
	Status InvestigationStatus
}

func (investigation *Investigation) Transition(to InvestigationStatus) error {
	if investigation == nil {
		return &TransitionError{To: to}
	}
	allowed := investigationTransitions[investigation.Status]
	if _, ok := allowed[to]; !ok {
		return &TransitionError{From: investigation.Status, To: to}
	}
	investigation.Status = to
	return nil
}

type DeploymentMatchStatus string

const (
	DeploymentMatchPending   DeploymentMatchStatus = "PENDING"
	DeploymentMatchMatched   DeploymentMatchStatus = "MATCHED"
	DeploymentMatchAmbiguous DeploymentMatchStatus = "AMBIGUOUS"
	DeploymentMatchMissing   DeploymentMatchStatus = "MISSING"
)

func (status DeploymentMatchStatus) Valid() bool {
	switch status {
	case DeploymentMatchPending, DeploymentMatchMatched, DeploymentMatchAmbiguous, DeploymentMatchMissing:
		return true
	default:
		return false
	}
}

type IncidentStatus string

const (
	IncidentOpen          IncidentStatus = "OPEN"
	IncidentInvestigating IncidentStatus = "INVESTIGATING"
	IncidentResolved      IncidentStatus = "RESOLVED"
	IncidentClosed        IncidentStatus = "CLOSED"
)

func (status IncidentStatus) Valid() bool {
	switch status {
	case IncidentOpen, IncidentInvestigating, IncidentResolved, IncidentClosed:
		return true
	default:
		return false
	}
}

type NotificationStatus string

const (
	NotificationPending NotificationStatus = "PENDING"
	NotificationSending NotificationStatus = "SENDING"
	NotificationSent    NotificationStatus = "SENT"
	NotificationFailed  NotificationStatus = "FAILED"
)

func (status NotificationStatus) Valid() bool {
	switch status {
	case NotificationPending, NotificationSending, NotificationSent, NotificationFailed:
		return true
	default:
		return false
	}
}
