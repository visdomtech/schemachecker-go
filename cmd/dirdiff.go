package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/checkererror"
	"github.com/visdomtech/schemachecker-go/internal/dirdiff"
)

// RunDirDiff executes the dirdiff subcommand.
// Usage: dirdiff [left] [right]
func RunDirDiff(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage dirdiff [left] [right]")
	}

	differ, err := dirdiff.New(args[1], args[2], nil)
	if err != nil {
		return err
	}

	differ.Dump()
	if differ.IsSame() {
		return nil
	}

	return checkererror.New(1, "The directories are not the same")
}
