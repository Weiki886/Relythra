package diagnosiscase

import (
	"errors"
	"fmt"
	"io/fs"
)

var (
	ErrAccessDenied     = errors.New("evaluation case access denied")
	ErrAuditUnavailable = errors.New("evaluation access audit unavailable")
	ErrInvalidPath      = errors.New("invalid evaluation case path")
)

type Principal string

const (
	Runtime   Principal = "runtime"
	Evaluator Principal = "evaluator"
)

type AccessDenied struct {
	Principal Principal
	Path      string
	Reason    string
}

type AuditFunc func(AccessDenied)

func Read(filesystem fs.FS, name string, principal Principal, audit AuditFunc) (Case, error) {
	if !fs.ValidPath(name) {
		return Case{}, fmt.Errorf("%w: %q", ErrInvalidPath, name)
	}
	if principal != Evaluator {
		if audit == nil {
			return Case{}, ErrAuditUnavailable
		}
		audit(AccessDenied{Principal: principal, Path: name, Reason: "runtime principals cannot read evaluation truth"})
		return Case{}, ErrAccessDenied
	}
	if filesystem == nil {
		return Case{}, errors.New("evaluation case filesystem is required")
	}
	data, err := fs.ReadFile(filesystem, name)
	if err != nil {
		return Case{}, fmt.Errorf("read evaluation case: %w", err)
	}
	caseFile, err := Decode(data)
	if err != nil {
		return Case{}, err
	}
	return caseFile, nil
}
