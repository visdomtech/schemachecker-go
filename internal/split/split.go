// Package split implements the PGDumpSplitter state machine that parses
// pg_dump output and splits it into per-object SQL files with an index.txt.
package split

import (
	"bufio"
	"fmt"
	"io"
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

// Dump splits the pg_dump file at dumpFile into per-object SQL files under destDir.
func Dump(dumpFile string, destDir string) error {
	f, err := os.Open(dumpFile)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer f.Close()

	buf := newDumpBuffer(destDir)
	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for large SQL lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		switch buf.state {
		case stateEmpty:
			if buf.processComment(line) {
				// handled
			} else if strings.TrimSpace(line) == "" {
				// skip blank
			} else {
				buf.flushTo(stateSettings, "SETTINGS.sql", "-- Beginning of dump")
				buf.append(line)
			}
		case stateSettings, stateDef, stateInsert:
			if buf.processComment(line) {
				// handled
			} else {
				buf.append(line)
			}
		case stateData:
			if strings.HasPrefix(line, "COPY ") {
				buf.state = stateCopy
			} else if strings.HasPrefix(line, "INSERT ") {
				buf.state = stateInsert
			}
			buf.append(line)
		case stateCopy:
			buf.append(line)
			if line == `\.` {
				if buf.numLines() == 4 { // 2 comments + COPY + \.
					buf.setNewState(stateEmpty, "", "-- avoid creating empty data files")
				} else {
					buf.flushTo(stateEmpty, "", "")
				}
			}
		case stateSeqSet:
			if buf.processComment(line) {
				// handled
			} else if strings.HasPrefix(line, "SELECT pg_catalog.setval") {
				if reSeqSetDefault.MatchString(line) {
					buf.setNewState(stateEmpty, "", "-- avoid creating default seq files")
				} else {
					buf.append(line)
				}
			} else {
				buf.append(line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan dump file: %w", err)
	}

	// flush final buffer
	buf.flushTo(stateEmpty, "", "-- flushing last buff at end of file")
	return nil
}

// dumpBuffer accumulates lines and flushes them to per-object SQL files.
type dumpBuffer struct {
	destDir string
	lines   []string
	state   state
	title   string
	fname   string
}

func newDumpBuffer(destDir string) *dumpBuffer {
	return &dumpBuffer{
		destDir: destDir,
		state:   stateEmpty,
		title:   "-- Start of split",
	}
}

func (b *dumpBuffer) setNewState(s state, fname, title string) {
	b.lines = nil
	b.state = s
	b.fname = fname
	b.title = title
}

func (b *dumpBuffer) append(line string) {
	b.lines = append(b.lines, line)
}

func (b *dumpBuffer) numLines() int {
	return len(b.lines)
}

// processComment checks if line is a pg_dump OBJDESC comment and, if so,
// flushes the current buffer and starts a new file.
func (b *dumpBuffer) processComment(line string) bool {
	if !strings.HasPrefix(line, "--") {
		return false
	}
	match := reObjDesc.FindStringSubmatch(line)
	if match == nil {
		return false
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

	filename := resolveFilename(objType, schema, name, refName)

	if len(filename) > 255 {
		filename = filename[:251] + ".sql"
	}

	b.flushTo(b.state, filename, line)
	return true
}

func resolveFilename(objType, schema, name, refName string) string {
	if schema == "-" && objType == "EXTENSION" {
		return fmt.Sprintf("EXTENSION/%s.sql", name)
	}
	if schema == "-" && objType == "COMMENT" {
		return fmt.Sprintf("EXTENSION/%s.sql", name)
	}
	if schema == "-" && objType == "ACL" {
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

func (b *dumpBuffer) flushTo(newState state, newFname, newTitle string) {
	// Trim leading comments and blank lines
	for len(b.lines) > 0 && (strings.TrimSpace(b.lines[0]) == "" || strings.HasPrefix(b.lines[0], "--")) {
		b.lines = b.lines[1:]
	}
	// Trim trailing comments and blank lines
	for len(b.lines) > 0 && (strings.TrimSpace(b.lines[len(b.lines)-1]) == "" || strings.HasPrefix(b.lines[len(b.lines)-1], "--")) {
		b.lines = b.lines[:len(b.lines)-1]
	}

	if len(b.lines) > 0 && b.fname != "" {
		filePath := filepath.Join(b.destDir, b.fname)
		dirPath := filepath.Dir(filePath)

		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			// best-effort: log and continue like Java version
			fmt.Fprintf(os.Stderr, "warning: mkdir %s: %v\n", dirPath, err)
		}

		indexPath := filepath.Join(b.destDir, "index.txt")

		// Create index.txt if it doesn't exist
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			if f, err := os.Create(indexPath); err == nil {
				f.Close()
			}
		}

		isNew := false
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			isNew = true
			if f, err := os.Create(filePath); err == nil {
				f.Close()
			}
		}

		// Append to index when creating new file (preserves build order)
		if isNew {
			idxFile, err := os.OpenFile(indexPath, os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				fmt.Fprintln(idxFile, b.fname)
				idxFile.Close()
			}
		}

		outFile, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			w := bufio.NewWriter(outFile)
			for _, line := range b.lines {
				fmt.Fprintln(w, line)
			}
			w.Flush()
			outFile.Close()
		}
	}

	b.setNewState(newState, newFname, newTitle)
}

// ReadAll reads all content from a file, returning it as a string.
func ReadAll(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
