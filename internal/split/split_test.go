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
		{"SCHEMA", "-", "public", "", "SCHEMAS/public.sql"},
		{"TYPE", "-", "sometype", "", "SCHEMAS/sometype.sql"},

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
	if err := Dump(dumpFile, destDir, Options{}); err != nil {
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
	if err := Dump(dumpFile, destDir, Options{}); err != nil {
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
	if err := Dump(dumpFile, destDir, Options{}); err != nil {
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
	if err := Dump(dumpFile, destDir, Options{}); err != nil {
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
	err := Dump(dumpFile, destDir, Options{})
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

func TestMergeTableObjects(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "output")

	// Create directory structure mimicking a split dump.
	dirs := []string{
		"public/TABLE",
		"public/INDEX",
		"public/TRIGGER",
		"public/DEFAULT",
		"public/FK_CONSTRAINT",
		"public/CONSTRAINT",
		"public/FUNCTION",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		// TABLE files
		"public/TABLE/users.sql":  "CREATE TABLE public.users (\n    id integer NOT NULL,\n    name text\n);\n",
		"public/TABLE/orders.sql": "CREATE TABLE public.orders (\n    id integer NOT NULL,\n    user_id integer\n);\n",

		// INDEX files: one named after table, one named after the index
		"public/INDEX/users.sql":           "CREATE INDEX users_name_idx ON users(name);\n",
		"public/INDEX/orders_user_id_idx.sql": "CREATE INDEX orders_user_id_idx ON public.orders(user_id);\n",

		// TRIGGER files
		"public/TRIGGER/users.sql": "CREATE TRIGGER users_audit AFTER UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE audit();\n",

		// DEFAULT files
		"public/DEFAULT/orders.sql": "ALTER TABLE public.orders ALTER COLUMN id SET DEFAULT nextval('orders_id_seq');\n",

		// FK_CONSTRAINT files
		"public/FK_CONSTRAINT/orders.sql": "ALTER TABLE public.orders ADD CONSTRAINT orders_user_fkey FOREIGN KEY (user_id) REFERENCES users(id);\n",

		// CONSTRAINT files
		"public/CONSTRAINT/users.sql": "ALTER TABLE public.users ADD CONSTRAINT users_name_not_empty CHECK (name <> '');\n",

		// FUNCTION files (should NOT be merged)
		"public/FUNCTION/audit.sql": "CREATE FUNCTION audit() RETURNS trigger AS $$ BEGIN END $$ LANGUAGE plpgsql;\n",

		// index.txt
		"index.txt": strings.Join([]string{
			"public/TABLE/users.sql",
			"public/TABLE/orders.sql",
			"public/INDEX/users.sql",
			"public/INDEX/orders_user_id_idx.sql",
			"public/TRIGGER/users.sql",
			"public/DEFAULT/orders.sql",
			"public/FK_CONSTRAINT/orders.sql",
			"public/CONSTRAINT/users.sql",
			"public/FUNCTION/audit.sql",
		}, "\n") + "\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(destDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := MergeTableObjects(destDir); err != nil {
		t.Fatalf("MergeTableObjects failed: %v", err)
	}

	// Verify TABLE/users.sql contains merged content
	usersData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	usersSQL := string(usersData)
	for _, want := range []string{
		"CREATE TABLE public.users",
		"CREATE INDEX users_name_idx",
		"CREATE TRIGGER users_audit",
		"CHECK (name <> '')",
	} {
		if !strings.Contains(usersSQL, want) {
			t.Errorf("users.sql should contain %q", want)
		}
	}

	// Verify TABLE/orders.sql contains merged content
	ordersData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/orders.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ordersSQL := string(ordersData)
	for _, want := range []string{
		"CREATE TABLE public.orders",
		"CREATE INDEX orders_user_id_idx",
		"ALTER COLUMN id SET DEFAULT",
		"orders_user_fkey FOREIGN KEY",
	} {
		if !strings.Contains(ordersSQL, want) {
			t.Errorf("orders.sql should contain %q", want)
		}
	}

	// Verify merged files are removed
	removed := []string{
		"public/INDEX/users.sql",
		"public/INDEX/orders_user_id_idx.sql",
		"public/TRIGGER/users.sql",
		"public/DEFAULT/orders.sql",
		"public/FK_CONSTRAINT/orders.sql",
		"public/CONSTRAINT/users.sql",
	}
	for _, relPath := range removed {
		if _, err := os.Stat(filepath.Join(destDir, relPath)); !os.IsNotExist(err) {
			t.Errorf("merged file %s should be removed", relPath)
		}
	}

	// Verify FUNCTION file is NOT merged
	if _, err := os.Stat(filepath.Join(destDir, "public/FUNCTION/audit.sql")); os.IsNotExist(err) {
		t.Error("FUNCTION/audit.sql should NOT be removed")
	}

	// Verify index.txt is updated: merged entries removed, TABLE and FUNCTION entries kept
	indexData, err := os.ReadFile(filepath.Join(destDir, "index.txt"))
	if err != nil {
		t.Fatal(err)
	}
	indexContent := string(indexData)
	for _, want := range []string{
		"public/TABLE/users.sql",
		"public/TABLE/orders.sql",
		"public/FUNCTION/audit.sql",
	} {
		if !strings.Contains(indexContent, want) {
			t.Errorf("index.txt should still contain %q", want)
		}
	}
	for _, notWant := range []string{
		"public/INDEX/",
		"public/TRIGGER/",
		"public/DEFAULT/",
		"public/FK_CONSTRAINT/",
		"public/CONSTRAINT/",
	} {
		if strings.Contains(indexContent, notWant) {
			t.Errorf("index.txt should NOT contain %q", notWant)
		}
	}
}

func TestDumpWithMerge(t *testing.T) {
	tmpDir := t.TempDir()
	dumpFile := filepath.Join(tmpDir, "dump.sql")

	// Synthetic pg_dump with TABLE + INDEX + TRIGGER
	dumpContent := strings.Join([]string{
		"-- Name: users; Type: TABLE; Schema: public; Owner: postgres",
		"",
		"CREATE TABLE public.users (",
		"    id integer NOT NULL,",
		"    email text",
		");",
		"",
		"-- Name: users_email_idx; Type: INDEX; Schema: public; Owner: postgres",
		"",
		"CREATE INDEX users_email_idx ON public.users(email);",
		"",
		"-- Name: audit_trigger users; Type: TRIGGER; Schema: public; Owner: postgres",
		"",
		"CREATE TRIGGER audit_trigger AFTER UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE audit();",
		"",
	}, "\n")

	if err := os.WriteFile(dumpFile, []byte(dumpContent), 0o644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(tmpDir, "output")
	if err := Dump(dumpFile, destDir, Options{Merge: true}); err != nil {
		t.Fatalf("Dump with merge failed: %v", err)
	}

	// TABLE/users.sql should contain TABLE + INDEX + TRIGGER
	usersData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	usersSQL := string(usersData)
	if !strings.Contains(usersSQL, "CREATE TABLE public.users") {
		t.Error("users.sql should contain CREATE TABLE")
	}
	if !strings.Contains(usersSQL, "CREATE INDEX users_email_idx") {
		t.Error("users.sql should contain merged INDEX")
	}
	if !strings.Contains(usersSQL, "CREATE TRIGGER audit_trigger") {
		t.Error("users.sql should contain merged TRIGGER")
	}

	// Separate INDEX and TRIGGER files should not exist
	if _, err := os.Stat(filepath.Join(destDir, "public/INDEX/users_email_idx.sql")); !os.IsNotExist(err) {
		t.Error("INDEX file should be removed after merge")
	}
	if _, err := os.Stat(filepath.Join(destDir, "public/TRIGGER/users.sql")); !os.IsNotExist(err) {
		t.Error("TRIGGER file should be removed after merge")
	}

	// Empty directories should be removed after merge
	for _, dir := range []string{"public/INDEX", "public/TRIGGER", "public/DEFAULT", "public/FK_CONSTRAINT", "public/CONSTRAINT"} {
		if _, err := os.Stat(filepath.Join(destDir, dir)); !os.IsNotExist(err) {
			t.Errorf("empty directory %s should be removed after merge", dir)
		}
	}

	// Non-empty directories should still exist
	if _, err := os.Stat(filepath.Join(destDir, "public/TABLE")); os.IsNotExist(err) {
		t.Error("TABLE directory should still exist")
	}
}

func TestMergeInlineSequence(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "output")

	// Create directory structure with a table-owned sequence.
	for _, d := range []string{"public/TABLE", "public/SEQUENCE", "public/DEFAULT"} {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		"public/TABLE/users.sql": "CREATE TABLE public.users (\n    id integer NOT NULL,\n    name text\n);\n",
		"public/SEQUENCE/users_id_seq.sql": strings.Join([]string{
			"ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;",
			"",
			"CREATE SEQUENCE public.users_id_seq",
			"    START WITH 1",
			"    INCREMENT BY 1",
			"    NO MINVALUE",
			"    NO MAXVALUE",
			"    CACHE 1;",
		}, "\n") + "\n",
		"public/DEFAULT/users.sql": "ALTER TABLE public.users ALTER COLUMN id SET DEFAULT nextval('users_id_seq'::regclass);\n",
		"index.txt": strings.Join([]string{
			"public/TABLE/users.sql",
			"public/SEQUENCE/users_id_seq.sql",
			"public/DEFAULT/users.sql",
		}, "\n") + "\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(destDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := MergeTableObjects(destDir); err != nil {
		t.Fatalf("MergeTableObjects failed: %v", err)
	}

	// TABLE/users.sql should have id as bigserial instead of integer
	usersData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	usersSQL := string(usersData)
	if !strings.Contains(usersSQL, "bigserial") {
		t.Errorf("users.sql should contain 'bigserial', got:\n%s", usersSQL)
	}
	if strings.Contains(usersSQL, "integer NOT NULL") {
		t.Error("users.sql should NOT contain 'integer NOT NULL' after inlining")
	}
	if strings.Contains(usersSQL, "nextval") {
		t.Error("users.sql should NOT contain 'nextval' default after inlining")
	}

	// SEQUENCE file should be removed (standard sequence inlined)
	if _, err := os.Stat(filepath.Join(destDir, "public/SEQUENCE/users_id_seq.sql")); !os.IsNotExist(err) {
		t.Error("SEQUENCE file should be removed after inlining")
	}

	// DEFAULT file should be removed (nextval line removed)
	if _, err := os.Stat(filepath.Join(destDir, "public/DEFAULT/users.sql")); !os.IsNotExist(err) {
		t.Error("DEFAULT file should be removed after inlining nextval")
	}
}

func TestMergeInlineSequenceNonStandard(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "output")

	// Non-standard sequence (with CYCLE) should NOT be inlined.
	for _, d := range []string{"public/TABLE", "public/SEQUENCE"} {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		"public/TABLE/items.sql": "CREATE TABLE public.items (\n    id integer NOT NULL\n);\n",
		"public/SEQUENCE/items_id_seq.sql": strings.Join([]string{
			"ALTER SEQUENCE public.items_id_seq OWNED BY public.items.id;",
			"",
			"CREATE SEQUENCE public.items_id_seq",
			"    START WITH 0",
			"    INCREMENT BY 1",
			"    MINVALUE 0",
			"    MAXVALUE 999999",
			"    CACHE 1",
			"    CYCLE;",
		}, "\n") + "\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(destDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := MergeTableObjects(destDir); err != nil {
		t.Fatalf("MergeTableObjects failed: %v", err)
	}

	// TABLE should be unchanged (non-standard sequence)
	itemsData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/items.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(itemsData), "bigserial") {
		t.Error("non-standard sequence should NOT be inlined")
	}

	// SEQUENCE file should still exist
	if _, err := os.Stat(filepath.Join(destDir, "public/SEQUENCE/items_id_seq.sql")); os.IsNotExist(err) {
		t.Error("non-standard SEQUENCE file should be preserved")
	}
}

func TestMergeInlineSequenceBigint(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "output")

	for _, d := range []string{"public/TABLE", "public/SEQUENCE", "public/DEFAULT"} {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		"public/TABLE/orders.sql": "CREATE TABLE public.orders (\n    id bigint NOT NULL,\n    total numeric\n);\n",
		"public/SEQUENCE/orders_id_seq.sql": strings.Join([]string{
			"ALTER SEQUENCE public.orders_id_seq OWNED BY public.orders.id;",
			"",
			"CREATE SEQUENCE public.orders_id_seq",
			"    START WITH 1",
			"    INCREMENT BY 1",
			"    NO MINVALUE",
			"    NO MAXVALUE",
			"    CACHE 1;",
		}, "\n") + "\n",
		"public/DEFAULT/orders.sql": "ALTER TABLE ONLY public.orders ALTER COLUMN id SET DEFAULT nextval('orders_id_seq'::regclass);\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(destDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := MergeTableObjects(destDir); err != nil {
		t.Fatalf("MergeTableObjects failed: %v", err)
	}

	// bigint should also be converted to bigserial
	ordersData, err := os.ReadFile(filepath.Join(destDir, "public/TABLE/orders.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ordersData), "bigserial") {
		t.Errorf("bigint column should be converted to bigserial, got:\n%s", string(ordersData))
	}
}
