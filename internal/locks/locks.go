package locks

import "fmt"

type Lock interface {
	Release() error
}

type BusyError struct{ Path string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("another cagent operation holds lock %q", e.Path)
}
