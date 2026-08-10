// Package checkererror provides the shared error type for schemachecker
// subcommands, mirroring the Java SchemaScheckerError.
package checkererror

import "fmt"

// Error is a schemachecker error carrying an exit code.
type Error struct {
	ExitCode int
	Message  string
}

func (e *Error) Error() string {
	return e.Message
}

// New creates a new Error with the given exit code and formatted message.
func New(exitCode int, format string, args ...any) *Error {
	return &Error{
		ExitCode: exitCode,
		Message:  fmt.Sprintf(format, args...),
	}
}
