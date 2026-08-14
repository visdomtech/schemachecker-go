package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/visdomtech/schemachecker-go/cmd"
	"github.com/visdomtech/schemachecker-go/internal/checkererror"
)

// Version and GitSHA are set at build time via -ldflags.
// Falls back to "dev" / "unknown" if not set.
var (
	Version = "dev"
	GitSHA  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		cmd.PrintUsage()
		os.Exit(checkererror.ExitUsage)
	}

	args := os.Args[1:]

	// Handle --version / -v before subcommand dispatch
	if args[0] == "--version" || args[0] == "-v" {
		fmt.Printf("%s (%s)\n", Version, GitSHA)
		return
	}

	var err error
	switch args[0] {
	case "validate":
		err = cmd.RunValidate(args)
	case "check":
		err = cmd.RunCheck(args)
	case "split":
		err = cmd.RunSplit(args)
	case "merge":
		err = cmd.RunMerge(args)
	case "orphaned":
		err = cmd.RunOrphaned(args)
	case "dirdiff":
		err = cmd.RunDirDiff(args)
	case "diff":
		err = cmd.RunDiff(args)
	case "dump":
		err = cmd.RunDump(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", args[0])
		cmd.PrintUsage()
		os.Exit(checkererror.ExitUsage)
	}

	if err != nil {
		var ce *checkererror.Error
		if errors.As(err, &ce) {
			fmt.Fprintf(os.Stderr, "Err: %s\n", ce.Message)
			os.Exit(ce.ExitCode)
		}
		fmt.Fprintf(os.Stderr, "Err: %s\n", err)
		os.Exit(checkererror.ExitInfra)
	}
}
