// Package dirdiff compares two directory trees and reports added, removed,
// and changed files with unified diff output.
package dirdiff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// DeltaType represents the kind of change.
type DeltaType int

const (
	Added DeltaType = iota
	Removed
	Changed
)

// Delta represents a single difference between two directory trees.
type Delta struct {
	Type  DeltaType
	Left  string
	Right string
	Diff  string
}

// FileTreeDiffer compares two directory trees.
type FileTreeDiffer struct {
	Left           string
	Right          string
	IgnorePatterns []string
	Deltas         []Delta
}

// New creates a FileTreeDiffer and immediately runs the comparison.
func New(left, right string, ignorePatterns []string) (*FileTreeDiffer, error) {
	d := &FileTreeDiffer{
		Left:           left,
		Right:          right,
		IgnorePatterns: ignorePatterns,
	}
	if err := d.check(); err != nil {
		return nil, err
	}
	return d, nil
}

// IsSame returns true if there are no differences.
func (d *FileTreeDiffer) IsSame() bool {
	return len(d.Deltas) == 0
}

// Dump prints the differences to stdout.
func (d *FileTreeDiffer) Dump() {
	fmt.Printf("\nAdded (NOT in %s, in %s):\n", d.Left, d.Right)
	for _, delta := range d.Deltas {
		if delta.Type == Added {
			fmt.Printf("+ %s\n", delta.Right)
		}
	}

	fmt.Printf("\nRemoved (in %s, NOT in %s):\n", d.Left, d.Right)
	for _, delta := range d.Deltas {
		if delta.Type == Removed {
			fmt.Printf("- %s\n", delta.Left)
		}
	}

	fmt.Println("\nChanged:")
	for _, delta := range d.Deltas {
		if delta.Type == Changed {
			fmt.Println(delta.Diff)
		}
	}
}

// Summary returns a human-readable count of differences by type.
func (d *FileTreeDiffer) Summary() string {
	var added, removed, changed int
	for _, delta := range d.Deltas {
		switch delta.Type {
		case Added:
			added++
		case Removed:
			removed++
		case Changed:
			changed++
		}
	}
	return fmt.Sprintf("%d added, %d removed, %d changed", added, removed, changed)
}

// psqlMetaCommands lists the psql meta commands that should be treated as
// equivalent when both sides start with the same command.
var psqlMetaCommands = []string{`\restrict`, `\unrestrict`}

// normalizePSQLMetaCommand replaces the arguments of known psql meta commands
// with a canonical form so that diff treats them as equal.
func normalizePSQLMetaCommand(line string) string {
	for _, cmd := range psqlMetaCommands {
		if strings.HasPrefix(line, cmd) {
			return cmd
		}
	}
	return line
}

func (d *FileTreeDiffer) check() error {
	leftFiles, err := walkFiles(d.Left)
	if err != nil {
		return err
	}
	rightFiles, err := walkFiles(d.Right)
	if err != nil {
		return err
	}

	rightSet := toSet(rightFiles)
	leftSet := toSet(leftFiles)

	for _, lf := range leftFiles {
		if !rightSet[lf] {
			d.Deltas = append(d.Deltas, Delta{Type: Removed, Left: lf})
		} else {
			delta, err := d.diffFile(lf)
			if err != nil {
				return err
			}
			if delta != nil {
				d.Deltas = append(d.Deltas, *delta)
			}
		}
	}

	for _, rf := range rightFiles {
		if !leftSet[rf] {
			d.Deltas = append(d.Deltas, Delta{Type: Added, Right: rf})
		}
	}

	return nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (d *FileTreeDiffer) diffFile(file string) (*Delta, error) {
	leftPath := filepath.Join(d.Left, file)
	rightPath := filepath.Join(d.Right, file)

	leftContent, err := readFile(leftPath)
	if err != nil {
		return nil, err
	}
	rightContent, err := readFile(rightPath)
	if err != nil {
		return nil, err
	}

	text1 := filterLines(splitLines(leftContent), d.IgnorePatterns)
	text2 := filterLines(splitLines(rightContent), d.IgnorePatterns)

	// Normalize PSQL meta commands for comparison: replace args with canonical form.
	// This ensures that lines like `\restrict foo` and `\restrict bar` are treated as equal.
	norm1 := make([]string, len(text1))
	norm2 := make([]string, len(text2))
	for i, l := range text1 {
		norm1[i] = normalizePSQLMetaCommand(l)
	}
	for i, l := range text2 {
		norm2[i] = normalizePSQLMetaCommand(l)
	}

	// First check if normalized content is identical
	if equal(norm1, norm2) {
		return nil, nil
	}

	// Use go-difflib for unified diff on the normalized content
	ud := difflib.UnifiedDiff{
		A:        norm1,
		B:        norm2,
		FromFile: leftPath,
		ToFile:   rightPath,
		Context:  0,
	}
	result, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return nil, err
	}

	if result == "" {
		return nil, nil
	}

	return &Delta{
		Type:  Changed,
		Left:  leftPath,
		Right: rightPath,
		Diff:  result,
	}, nil
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func filterLines(lines []string, patterns []string) []string {
	if len(patterns) == 0 {
		return lines
	}
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		keep := true
		for _, p := range patterns {
			if strings.Contains(line, p) {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, line)
		}
	}
	return result
}

func walkFiles(dir string) ([]string, error) {
	var result []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		result = append(result, rel)
		return nil
	})
	sort.Strings(result)
	return result, err
}

func toSet(slice []string) map[string]bool {
	m := make(map[string]bool, len(slice))
	for _, s := range slice {
		m[s] = true
	}
	return m
}

// splitLines splits content on newlines, stripping a trailing newline to avoid
// a spurious empty element (matching Java's .lines() behavior).
func splitLines(content string) []string {
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

// DiffFiles compares two files and returns a unified diff string.
// This is used by the validate command for comparing dump files.
// It normalizes PSQL meta-commands (e.g. \restrict, \unrestrict) so that
// differences in their arguments are ignored, matching the directory-diff behavior.
func DiffFiles(file1, file2, label1, label2 string) (string, bool, error) {
	content1, err := readFile(file1)
	if err != nil {
		return "", false, err
	}
	content2, err := readFile(file2)
	if err != nil {
		return "", false, err
	}

	raw1 := splitLines(content1)
	raw2 := splitLines(content2)

	// Normalize PSQL meta commands for parity with directory diff
	text1 := make([]string, len(raw1))
	text2 := make([]string, len(raw2))
	for i, l := range raw1 {
		text1[i] = normalizePSQLMetaCommand(l)
	}
	for i, l := range raw2 {
		text2[i] = normalizePSQLMetaCommand(l)
	}

	ud := difflib.UnifiedDiff{
		A:        text1,
		B:        text2,
		FromFile: label1,
		ToFile:   label2,
		Context:  0,
	}
	result, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return "", false, err
	}

	return result, result == "", nil
}
