package cmd

import (
	"github.com/visdomtech/schemachecker-go/internal/merge"
)

// RunMerge executes the merge subcommand.
// Usage: merge [indexfile] [migrationfile]
func RunMerge(args []string) error {
	if len(args) != 3 {
		return UsageError("Invalid command line arguments.\nUsage merge [indexfile] [migrationfile]")
	}
	return merge.FromIndex(args[1], args[2])
}
