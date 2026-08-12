// Package split implements the PGDumpSplitter state machine that parses
// pg_dump output and splits it into per-object SQL files with an index.txt.
package split

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	reObjDesc = regexp.MustCompile(
		`^-- (?P<isData>Data for )?Name: (?P<name>.*?); ` +
			`Type: (?P<type>.*?); ` +
			`Schema: (?P<schema>.*?); ` +
			`Owner: (?P<owner>.*)$`)
	reSeqSet        = regexp.MustCompile(`^SELECT pg_catalog\.setval\('(?P<name>.*?)'.*`)
	reSeqSetDefault = regexp.MustCompile(`^SELECT pg_catalog\.setval\('(?P<name>.*?)', 1, false\);$`)
	reNameSplit     = regexp.MustCompile(`^(?P<ref>.*?)\s(?P<name>.*)$`)
	reFunctionName  = regexp.MustCompile(`^(?P<basename>.*)\((?P<parameters>.*?)\)$`)
)

type state int

const (
	stateEmpty state = iota
	stateSettings
	stateDef
	stateData
	stateCopy
	stateInsert
	stateSeqSet
)

// Options configures the split behavior.
type Options struct {
	// Merge inlines INDEX, TRIGGER, DEFAULT, CONSTRAINT, and FK_CONSTRAINT
	// SQL into the corresponding TABLE file instead of creating separate files.
	Merge bool
}

// Dump splits the pg_dump file at dumpFile into per-object SQL files under destDir.
func Dump(dumpFile string, destDir string, opts Options) error {
	f, err := os.Open(dumpFile)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer f.Close()

	buf := newDumpBuffer(destDir)
	reader := bufio.NewReaderSize(f, 1024*1024)

	for {
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			break // EOF with no data
		}
		line = strings.TrimSuffix(line, "\n")

		if flushErr := buf.processLine(line); flushErr != nil {
			return fmt.Errorf("process line: %w", flushErr)
		}

		if err != nil {
			break // EOF after reading last line (already processed above)
		}
	}

	// flush final buffer
	if err := buf.flushTo(stateEmpty, "", "-- flushing last buff at end of file"); err != nil {
		return fmt.Errorf("final flush: %w", err)
	}

	if opts.Merge {
		if err := MergeTableObjects(destDir); err != nil {
			return fmt.Errorf("merge post-processing failed (unmerged split still available at %s): %w", destDir, err)
		}
	}
	return nil
}

// processLine processes a single line through the state machine.
func (b *dumpBuffer) processLine(line string) error {
	switch b.state {
	case stateEmpty:
		consumed, err := b.processComment(line)
		if err != nil {
			return err
		}
		if consumed {
			return nil
		}
		if strings.TrimSpace(line) == "" {
			return nil // skip blank
		}
		if err := b.flushTo(stateSettings, "SETTINGS.sql", "-- Beginning of dump"); err != nil {
			return err
		}
		b.append(line)
	case stateSettings, stateDef, stateInsert:
		consumed, err := b.processComment(line)
		if err != nil {
			return err
		}
		if !consumed {
			b.append(line)
		}
	case stateData:
		if strings.HasPrefix(line, "COPY ") {
			b.state = stateCopy
		} else if strings.HasPrefix(line, "INSERT ") {
			b.state = stateInsert
		}
		b.append(line)
	case stateCopy:
		b.append(line)
		if line == `\.` {
			if b.numLines() == 4 { // OBJDESC + COPY + blank + \. = empty COPY block
				b.setNewState(stateEmpty, "", "-- avoid creating empty data files")
			} else {
				if err := b.flushTo(stateEmpty, "", ""); err != nil {
					return err
				}
			}
		}
	case stateSeqSet:
		consumed, err := b.processComment(line)
		if err != nil {
			return err
		}
		if consumed {
			return nil
		}
		if strings.HasPrefix(line, "SELECT pg_catalog.setval") {
			if reSeqSetDefault.MatchString(line) {
				b.setNewState(stateEmpty, "", "-- avoid creating default seq files")
			} else {
				b.append(line)
			}
		} else {
			b.append(line)
		}
	}
	return nil
}

// dumpBuffer accumulates lines and flushes them to per-object SQL files.
type dumpBuffer struct {
	destDir       string
	lines         []string
	state         state
	headerComment string
	filename      string
}

func newDumpBuffer(destDir string) *dumpBuffer {
	return &dumpBuffer{
		destDir:       destDir,
		state:         stateEmpty,
		headerComment: "-- Start of split",
	}
}

func (b *dumpBuffer) setNewState(s state, filename, headerComment string) {
	b.lines = nil
	b.state = s
	b.filename = filename
	b.headerComment = headerComment
}

func (b *dumpBuffer) append(line string) {
	b.lines = append(b.lines, line)
}

func (b *dumpBuffer) numLines() int {
	return len(b.lines)
}

// processComment checks if line is a pg_dump OBJDESC comment and, if so,
// flushes the current buffer and starts a new file.
// Returns (true, nil) if the line was consumed as an object comment,
// (false, nil) if not a comment, or (false, err) on I/O failure.
func (b *dumpBuffer) processComment(line string) (bool, error) {
	if !strings.HasPrefix(line, "--") {
		return false, nil
	}
	match := reObjDesc.FindStringSubmatch(line)
	if match == nil {
		return false, nil
	}

	result := make(map[string]string)
	for i, name := range reObjDesc.SubexpNames() {
		if i != 0 && name != "" {
			result[name] = match[i]
		}
	}

	objType := result["type"]
	schema := result["schema"]
	refName := ""

	if objType == "SEQUENCE SET" {
		b.state = stateSeqSet
	} else if result["isData"] != "" {
		b.state = stateData
	} else {
		b.state = stateDef
	}

	var name string
	switch objType {
	case "COMMENT", "ACL", "CONSTRAINT", "FK CONSTRAINT", "DEFAULT", "TRIGGER", "POLICY":
		nm := reNameSplit.FindStringSubmatch(result["name"])
		if nm != nil {
			refName = nm[1]
			name = nm[2]
		} else {
			name = result["name"]
		}
	case "FUNCTION":
		fm := reFunctionName.FindStringSubmatch(result["name"])
		if fm != nil {
			name = fm[1]
		} else {
			name = result["name"]
		}
	default:
		name = result["name"]
	}

	filename := ResolveFilename(objType, schema, name, refName)

	if len(filename) > 255 {
		filename = filename[:251] + ".sql"
	}

	return true, b.flushTo(b.state, filename, line)
}

// ResolveFilename returns the relative file path for a pg_dump object.
func ResolveFilename(objType, schema, name, refName string) string {
	if schema == "-" && objType == "EXTENSION" {
		return fmt.Sprintf("EXTENSION/%s.sql", name)
	}
	if schema == "-" && objType == "COMMENT" {
		return fmt.Sprintf("EXTENSION/%s.sql", name)
	}
	// Any remaining schema-less objects (SCHEMA, ACL, etc.) go to SCHEMAS/.
	if schema == "-" {
		return fmt.Sprintf("SCHEMAS/%s.sql", name)
	}
	switch objType {
	case "CONSTRAINT":
		return fmt.Sprintf("%s/TABLE/%s.sql", schema, refName)
	case "FK CONSTRAINT":
		return fmt.Sprintf("%s/FK_CONSTRAINT/%s.sql", schema, refName)
	case "TRIGGER":
		return fmt.Sprintf("%s/TRIGGER/%s.sql", schema, refName)
	case "POLICY":
		return fmt.Sprintf("%s/POLICY/%s.sql", schema, refName)
	case "DEFAULT":
		return fmt.Sprintf("%s/DEFAULT/%s.sql", schema, refName)
	case "SEQUENCE OWNED BY":
		return fmt.Sprintf("%s/SEQUENCE/%s.sql", schema, name)
	case "FUNCTION":
		return fmt.Sprintf("%s/FUNCTION/%s.sql", schema, name)
	case "SEQUENCE SET":
		return fmt.Sprintf("%s/SEQUENCE_SET/%s.sql", schema, name)
	case "TABLE DATA":
		return fmt.Sprintf("%s/DATA/%s.sql", schema, name)
	default:
		return fmt.Sprintf("%s/%s/%s.sql", schema, objType, name)
	}
}

func (b *dumpBuffer) flushTo(newState state, newFilename, newHeaderComment string) error {
	// Trim leading comments and blank lines
	for len(b.lines) > 0 && (strings.TrimSpace(b.lines[0]) == "" || strings.HasPrefix(b.lines[0], "--")) {
		b.lines = b.lines[1:]
	}
	// Trim trailing comments and blank lines
	for len(b.lines) > 0 && (strings.TrimSpace(b.lines[len(b.lines)-1]) == "" || strings.HasPrefix(b.lines[len(b.lines)-1], "--")) {
		b.lines = b.lines[:len(b.lines)-1]
	}

	if len(b.lines) > 0 && b.filename != "" {
		filePath := filepath.Join(b.destDir, b.filename)

		// Path containment: ensure resolved path stays within destDir
		absDest, err := filepath.Abs(b.destDir)
		if err != nil {
			return fmt.Errorf("resolve destDir abs path: %w", err)
		}
		absFile, err := filepath.Abs(filePath)
		if err != nil {
			return fmt.Errorf("resolve file abs path: %w", err)
		}
		if !strings.HasPrefix(absFile, absDest+string(os.PathSeparator)) {
			fmt.Fprintf(os.Stderr, "warning: skipping file %q that escapes output directory\n", b.filename)
			b.setNewState(newState, newFilename, newHeaderComment)
			return nil
		}

		dirPath := filepath.Dir(filePath)

		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dirPath, err)
		}

		indexPath := filepath.Join(b.destDir, "index.txt")

		// Create index.txt if it doesn't exist
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			idxF, err := os.Create(indexPath)
			if err != nil {
				return fmt.Errorf("create index.txt: %w", err)
			}
			idxF.Close()
		}

		// Atomically test-and-create the target file using O_EXCL
		isNew := false
		if f, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			isNew = true
		}

		// Append to index when creating new file (preserves build order)
		if isNew {
			idxFile, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return fmt.Errorf("open index.txt for append: %w", err)
			}
			if _, err := fmt.Fprintln(idxFile, b.filename); err != nil {
				idxFile.Close()
				return fmt.Errorf("write index.txt: %w", err)
			}
			if err := idxFile.Close(); err != nil {
				return fmt.Errorf("close index.txt: %w", err)
			}
		}

		outFile, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open %s: %w", filePath, err)
		}
		w := bufio.NewWriter(outFile)
		for _, line := range b.lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				outFile.Close()
				return fmt.Errorf("write %s: %w", filePath, err)
			}
		}
		if err := w.Flush(); err != nil {
			outFile.Close()
			return fmt.Errorf("flush %s: %w", filePath, err)
		}
		if err := outFile.Close(); err != nil {
			return fmt.Errorf("close %s: %w", filePath, err)
		}
	}

	b.setNewState(newState, newFilename, newHeaderComment)
	return nil
}
