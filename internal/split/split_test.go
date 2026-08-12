package split

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveFilename(t *testing.T) {
	tests := []struct {
		objType  string
		schema   string
		name     string
		refName  string
		expected string
	}{
		// Schema "-" special cases
		{"EXTENSION", "-", "plpgsql", "", "EXTENSION/plpgsql.sql"},
		{"COMMENT", "-", "ext_comment", "", "EXTENSION/ext_comment.sql"},
		{"ACL", "-", "public", "", "SCHEMAS/public.sql"},

		// RefName-based types
		{"CONSTRAINT", "public", "users_pkey", "users", "public/TABLE/users.sql"},
		{"FK CONSTRAINT", "public", "orders_user_fk", "orders", "public/FK_CONSTRAINT/orders.sql"},
		{"TRIGGER", "public", "audit_trigger", "users", "public/TRIGGER/users.sql"},
		{"POLICY", "public", "user_policy", "users", "public/POLICY/users.sql"},
		{"DEFAULT", "public", "id_default", "users", "public/DEFAULT/users.sql"},

		// Name-based types
		{"SEQUENCE OWNED BY", "public", "users_id_seq", "", "public/SEQUENCE/users_id_seq.sql"},
		{"FUNCTION", "public", "my_func", "", "public/FUNCTION/my_func.sql"},
		{"SEQUENCE SET", "public", "users_id_seq", "", "public/SEQUENCE_SET/users_id_seq.sql"},
		{"TABLE DATA", "public", "users", "", "public/DATA/users.sql"},

		// Default (TABLE, SEQUENCE, VIEW, etc.)
		{"TABLE", "public", "users", "", "public/TABLE/users.sql"},
		{"SEQUENCE", "public", "users_id_seq", "", "public/SEQUENCE/users_id_seq.sql"},
		{"VIEW", "public", "user_view", "", "public/VIEW/user_view.sql"},
		{"INDEX", "public", "users_email_idx", "", "public/INDEX/users_email_idx.sql"},
	}

	for _, tt := range tests {
		t.Run(tt.objType+"/"+tt.name, func(t *testing.T) {
			result := ResolveFilename(tt.objType, tt.schema, tt.name, tt.refName)
			if result != tt.expected {
				t.Errorf("ResolveFilename(%q, %q, %q, %q) = %q, want %q",
					tt.objType, tt.schema, tt.name, tt.refName, result, tt.expected)
			}
		})
	}
}

func TestRegexObjDesc(t *testing.T) {
	tests := []struct {
		line    string
		matches bool
		isData  string
		name    string
		objType string
		schema  string
	}{
		{
			line:    "-- Name: users; Type: TABLE; Schema: public; Owner: postgres",
			matches: true, name: "users", objType: "TABLE", schema: "public",
		},
		{
			line:    "-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: postgres",
			matches: true, isData: "Data for ", name: "users", objType: "TABLE DATA", schema: "public",
		},
		{
			line:    "-- Name: plpgsql; Type: EXTENSION; Schema: -; Owner: postgres",
			matches: true, name: "plpgsql", objType: "EXTENSION", schema: "-",
		},
		{
			line:    "-- just a regular comment",
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.line[:min(40, len(tt.line))], func(t *testing.T) {
			match := reObjDesc.FindStringSubmatch(tt.line)
			if tt.matches && match == nil {
				t.Fatalf("expected match for %q", tt.line)
			}
			if !tt.matches && match != nil {
				t.Fatalf("expected no match for %q", tt.line)
			}
			if !tt.matches {
				return
			}

			result := make(map[string]string)
			for i, name := range reObjDesc.SubexpNames() {
				if i != 0 && name != "" {
					result[name] = match[i]
				}
			}

			if result["name"] != tt.name {
				t.Errorf("name = %q, want %q", result["name"], tt.name)
			}
			if result["type"] != tt.objType {
				t.Errorf("type = %q, want %q", result["type"], tt.objType)
			}
			if result["schema"] != tt.schema {
				t.Errorf("schema = %q, want %q", result["schema"], tt.schema)
			}
			if result["isData"] != tt.isData {
				t.Errorf("isData = %q, want %q", result["isData"], tt.isData)
			}
		})
	}
}

func TestRegexSeqSet(t *testing.T) {
	if !reSeqSet.MatchString("SELECT pg_catalog.setval('users_id_seq', 100, true);") {
		t.Error("reSeqSet should match setval with value")
	}
	if reSeqSetDefault.MatchString("SELECT pg_catalog.setval('users_id_seq', 100, true);") {
		t.Error("reSeqSetDefault should NOT match setval with non-default value")
	}
	if !reSeqSetDefault.MatchString("SELECT pg_catalog.setval('users_id_seq', 1, false);") {
		t.Error("reSeqSetDefault should match setval with default value (1, false)")
	}
}

func TestRegexNameSplit(t *testing.T) {
	match := reNameSplit.FindStringSubmatch("users_pkey users")
	if match == nil {
		t.Fatal("expected match")
	}
	if match[1] != "users_pkey" {
		t.Errorf("ref = %q, want %q", match[1], "users_pkey")
	}
	if match[2] != "users" {
		t.Errorf("name = %q, want %q", match[2], "users")
	}
}

func TestRegexFunctionName(t *testing.T) {
	match := reFunctionName.FindStringSubmatch("my_func(integer, text)")
	if match == nil {
		t.Fatal("expected match")
	}
	if match[1] != "my_func" {
		t.Errorf("basename = %q, want %q", match[1], "my_func")
	}
	if match[2] != "integer, text" {
		t.Errorf("parameters = %q, want %q", match[2], "integer, text")
	}

	// Function without parameters
	if reFunctionName.MatchString("simple_func") {
		t.Error("should not match function without parentheses")
	}
}

func TestDumpBasic(t *testing.T) {
	// Create a synthetic pg_dump file
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	dumpContent := strings.Join([]string{
		"--",
		"-- PostgreSQL database dump",
		"--",
		"",
		"SET statement_timeout = 0;",
		"SET lock_timeout = 0;",
		"",
		"-- Name: users; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.users (",
		"    id integer NOT NULL,",
		"    name text",
		");",
		"",
		"-- Name: orders; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.orders (",
		"    id integer NOT NULL,",
		"    user_id integer",
		");",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir); err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	// Check SETTINGS.sql exists
	settingsPath := filepath.Join(destDir, "SETTINGS.sql")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Error("SETTINGS.sql should exist")
	}

	// Check table files exist
	usersPath := filepath.Join(destDir, "public", "TABLE", "users.sql")
	if _, err := os.Stat(usersPath); os.IsNotExist(err) {
		t.Errorf("users.sql should exist at %s", usersPath)
	}

	ordersPath := filepath.Join(destDir, "public", "TABLE", "orders.sql")
	if _, err := os.Stat(ordersPath); os.IsNotExist(err) {
		t.Errorf("orders.sql should exist at %s", ordersPath)
	}

	// Check index.txt exists and contains entries
	indexPath := filepath.Join(destDir, "index.txt")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("index.txt should exist")
	}

	indexContent, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(indexContent), "SETTINGS.sql") {
		t.Error("index.txt should contain SETTINGS.sql")
	}
	if !strings.Contains(string(indexContent), "public/TABLE/users.sql") {
		t.Error("index.txt should contain public/TABLE/users.sql")
	}
	if !strings.Contains(string(indexContent), "public/TABLE/orders.sql") {
		t.Error("index.txt should contain public/TABLE/orders.sql")
	}

	// Verify users.sql content
	usersData, err := os.ReadFile(usersPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(usersData), "CREATE TABLE public.users") {
		t.Error("users.sql should contain CREATE TABLE statement")
	}
}

func TestDumpCopyData(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	dumpContent := strings.Join([]string{
		"-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: postgres",
		"",
		"COPY public.users (id, name) FROM stdin;",
		"1\tAlice",
		"2\tBob",
		`\.` + "\n",
		"-- Name: orders; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.orders (id integer);",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir); err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	// Check data file exists
	dataPath := filepath.Join(destDir, "public", "DATA", "users.sql")
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Errorf("DATA/users.sql should exist at %s", dataPath)
	}
}

func TestDumpEmptyCopy(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	// Empty COPY block: OBJDESC + COPY + blank + \. = 4 lines → should NOT create file
	dumpContent := strings.Join([]string{
		"-- Data for Name: empty_table; Type: TABLE DATA; Schema: public; Owner: postgres",
		"",
		"COPY public.empty_table (id) FROM stdin;",
		"",
		`\.` + "\n",
		"-- Name: orders; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.orders (id integer);",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir); err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	// Empty COPY should NOT create a data file
	dataPath := filepath.Join(destDir, "public", "DATA", "empty_table.sql")
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Error("empty COPY block should NOT create a data file")
	}
}

func TestDumpSeqSet(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	dumpContent := strings.Join([]string{
		"-- Name: users_id_seq; Type: SEQUENCE SET; Schema: public; Owner: postgres",
		"",
		"SELECT pg_catalog.setval('users_id_seq', 1, false);",
		"",
		"-- Name: orders; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.orders (id integer);",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir); err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	// Default seq set (1, false) should NOT create a file
	seqPath := filepath.Join(destDir, "public", "SEQUENCE_SET", "users_id_seq.sql")
	if _, err := os.Stat(seqPath); !os.IsNotExist(err) {
		t.Error("default SEQUENCE SET should NOT create a file")
	}
}

func TestDumpFilenameTruncation(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	longName := strings.Repeat("a", 300)
	dumpContent := strings.Join([]string{
		"-- Name: " + longName + "; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public." + longName + " (id integer);",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	err := Dump(dumpFile, destDir)
	if err != nil {
		t.Fatalf("Dump failed: %v", err)
	}

	// Verify file was created with truncated name
	entries, err := os.ReadDir(filepath.Join(destDir, "public", "TABLE"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Error("should have created at least one file")
	}
	for _, e := range entries {
		if len(e.Name()) > 255 {
			t.Errorf("filename %q exceeds 255 chars", e.Name())
		}
	}
}
