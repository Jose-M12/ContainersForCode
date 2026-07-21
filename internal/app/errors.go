package app

import "fmt"

type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

func usage(format string, args ...any) error {
	return &CodedError{Code: 2, Err: fmt.Errorf(format, args...)}
}
func policyError(format string, args ...any) error {
	return &CodedError{Code: 3, Err: fmt.Errorf(format, args...)}
}
func notFound(format string, args ...any) error {
	return &CodedError{Code: 4, Err: fmt.Errorf(format, args...)}
}

func ExitCode(err error) int {
	if coded, ok := err.(*CodedError); ok {
		return coded.Code
	}
	return 1
}
