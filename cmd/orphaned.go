package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/orphaned"
)

// RunOrphaned executes the orphaned subcommand.
// Usage: orphaned [indexfile]
func RunOrphaned(args []string) error {
	if len(args) != 2 {
		return UsageError("Invalid command line arguments.\nUsage orphaned [indexfile]")
	}
	if err := orphaned.FromIndex(args[1]); err != nil {
		return checkererror.New(1, "%s", err)
	}
	return nil
}
