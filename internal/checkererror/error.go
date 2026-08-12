// Package checkererror provides the shared error type for schemachecker
// subcommands, mirroring the Java SchemaScheckerError.
package checkererror

import "fmt"

// Exit code constants for schemachecker subcommands.
const (
	ExitDiff  = 1  // schemas differ or orphaned files found
	ExitInfra = 3  // infrastructure failure (pg_dump, DB provisioning, I/O)
	ExitUsage = 99 // invalid command-line arguments
)

// Error is a schemachecker error carrying an exit code.
type Error struct {
	ExitCode int
	Message  string
	Cause    error
}

func (e *Error) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause, supporting errors.Is/As chains.
func (e *Error) Unwrap() error {
	return e.Cause
}

// New creates a new Error with the given exit code and formatted message.
func New(exitCode int, format string, args ...any) *Error {
	return &Error{
		ExitCode: exitCode,
		Message:  fmt.Sprintf(format, args...),
	}
}

// Wrap creates a new Error with the given exit code, formatted message,
// and an underlying cause for error chain support.
func Wrap(exitCode int, cause error, format string, args ...any) *Error {
	return &Error{
		ExitCode: exitCode,
		Message:  fmt.Sprintf(format, args...),
		Cause:    cause,
	}
}
