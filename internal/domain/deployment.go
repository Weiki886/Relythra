package domain

import (
	"errors"
	"fmt"
)

var ErrInvalidDeploymentTransition = errors.New("invalid deployment transition")

type DeploymentStatus string

const (
	DeploymentPending    DeploymentStatus = "PENDING"
	DeploymentInProgress DeploymentStatus = "IN_PROGRESS"
	DeploymentSucceeded  DeploymentStatus = "SUCCEEDED"
	DeploymentFailed     DeploymentStatus = "FAILED"
	DeploymentRolledBack DeploymentStatus = "ROLLED_BACK"
	DeploymentUnknown    DeploymentStatus = "UNKNOWN"
)

var deploymentTransitions = map[DeploymentStatus]map[DeploymentStatus]struct{}{
	DeploymentPending: {
		DeploymentInProgress: {},
		DeploymentSucceeded:  {},
		DeploymentFailed:     {},
		DeploymentUnknown:    {},
	},
	DeploymentInProgress: {
		DeploymentSucceeded: {},
		DeploymentFailed:    {},
		DeploymentUnknown:   {},
	},
	DeploymentUnknown: {
		DeploymentInProgress: {},
		DeploymentSucceeded:  {},
		DeploymentFailed:     {},
	},
	DeploymentSucceeded: {
		DeploymentRolledBack: {},
	},
}

func (status DeploymentStatus) Valid() bool {
	if _, ok := deploymentTransitions[status]; ok {
		return true
	}
	switch status {
	case DeploymentFailed, DeploymentRolledBack:
		return true
	default:
		return false
	}
}

type DeploymentTransitionError struct {
	From DeploymentStatus
	To   DeploymentStatus
}

func (err *DeploymentTransitionError) Error() string {
	return fmt.Sprintf("cannot transition deployment from %q to %q", err.From, err.To)
}

func (err *DeploymentTransitionError) Unwrap() error {
	return ErrInvalidDeploymentTransition
}

type DeploymentEvent struct {
	Status DeploymentStatus
}

func (event *DeploymentEvent) Transition(to DeploymentStatus) error {
	if event == nil {
		return &DeploymentTransitionError{To: to}
	}
	allowed := deploymentTransitions[event.Status]
	if _, ok := allowed[to]; !ok {
		return &DeploymentTransitionError{From: event.Status, To: to}
	}
	event.Status = to
	return nil
}
