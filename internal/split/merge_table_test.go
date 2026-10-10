package split

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/visdomtech/schemachecker-go/internal/merge"
)

// buildGoldenFixture creates a realistic split directory with multiple tables,
// child objects, sequences (inlineable and non-inlineable), and an unresolvable
// index. It returns the destDir and a snapshot of all file contents before merge.
func buildGoldenFixture(t *testing.T) (destDir string, before map[string]string) {
	t.Helper()
	destDir = t.TempDir()

	// Schema directories
	for _, d := range []string{
		"public/TABLE", "public/INDEX", "public/TRIGGER",
		"public/DEFAULT", "public/FK_CONSTRAINT", "public/CONSTRAINT",
		"public/SEQUENCE",
	} {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	before = map[string]string{
		// --- TABLE files ---
		"public/TABLE/users.sql": strings.Join([]string{
			"CREATE TABLE public.users (",
			"    id integer NOT NULL,",
			"    name text",
			");",
			"",
		}, "\n"),
		"public/TABLE/orders.sql": strings.Join([]string{
			"CREATE TABLE public.orders (",
			"    id integer NOT NULL,",
			"    user_id integer",
			");",
			"",
		}, "\n"),
		"public/TABLE/items.sql": strings.Join([]string{
			"CREATE TABLE public.items (",
			"    id integer NOT NULL,",
			"    name text",
			");",
			"",
		}, "\n"),

		// --- INDEX files (some by table name, some by index name) ---
		"public/INDEX/users.sql":           "CREATE INDEX users_name_idx ON users(name);\n",
		"public/INDEX/orders_user_id_idx.sql": "CREATE INDEX orders_user_id_idx ON public.orders(user_id);\n",
		"public/INDEX/items_name_idx.sql":  "CREATE INDEX items_name_idx ON public.items(name);\n",
		// Unresolvable: ON ONLY (partitioned parent)
		"public/INDEX/unresolvable_parent_idx.sql": "CREATE INDEX unresolvable_parent_idx ON ONLY public.orders USING btree (id);\n",

		// --- TRIGGER files ---
		"public/TRIGGER/users.sql": "CREATE TRIGGER users_audit AFTER UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE audit();\n",

		// --- DEFAULT files ---
		"public/DEFAULT/orders.sql": "ALTER TABLE public.orders ALTER COLUMN id SET DEFAULT nextval('orders_id_seq');\n",
		"public/DEFAULT/items.sql":  "ALTER TABLE public.items ALTER COLUMN id SET DEFAULT nextval('items_id_seq');\n",

		// --- FK_CONSTRAINT files ---
		"public/FK_CONSTRAINT/orders.sql": "ALTER TABLE public.orders ADD CONSTRAINT orders_user_fkey FOREIGN KEY (user_id) REFERENCES users(id);\n",

		// --- CONSTRAINT files ---
		"public/CONSTRAINT/users.sql": "ALTER TABLE public.users ADD CONSTRAINT users_name_not_empty CHECK (name <> '');\n",

		// --- SEQUENCE: standard inlineable (bigserial-eligible) ---
		"public/SEQUENCE/users_id_seq.sql": strings.Join([]string{
			"ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;",
			"",
			"CREATE SEQUENCE public.users_id_seq",
			"    START WITH 1",
			"    INCREMENT BY 1",
			"    NO MINVALUE",
			"    NO MAXVALUE",
			"    CACHE 1;",
			"",
		}, "\n"),

		// --- SEQUENCE: non-standard (CYCLE) — NOT inlineable, N1 case ---
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
			"",
		}, "\n"),

		// --- index.txt (creation order from pg_dump) ---
		"index.txt": strings.Join([]string{
			"public/TABLE/users.sql",
			"public/TABLE/orders.sql",
			"public/TABLE/items.sql",
			"public/INDEX/users.sql",
			"public/INDEX/orders_user_id_idx.sql",
			"public/INDEX/items_name_idx.sql",
			"public/INDEX/unresolvable_parent_idx.sql",
			"public/TRIGGER/users.sql",
			"public/DEFAULT/orders.sql",
			"public/DEFAULT/items.sql",
			"public/FK_CONSTRAINT/orders.sql",
			"public/CONSTRAINT/users.sql",
			"public/SEQUENCE/users_id_seq.sql",
			"public/SEQUENCE/items_id_seq.sql",
		}, "\n") + "\n",
	}

	for relPath, content := range before {
		if err := os.WriteFile(filepath.Join(destDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return destDir, before
}

// readAllFiles reads every file under destDir (recursively) and returns a map
// of relative path → content. Empty directories are skipped.
func readAllFiles(t *testing.T, destDir string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			readAllFilesRecursive(t, destDir, entry.Name(), result)
		} else {
			data, err := os.ReadFile(filepath.Join(destDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			result[entry.Name()] = string(data)
		}
	}
	return result
}

func readAllFilesRecursive(t *testing.T, root, rel string, out map[string]string) {
	t.Helper()
	path := filepath.Join(root, rel)
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		relPath := filepath.Join(rel, entry.Name())
		if entry.IsDir() {
			readAllFilesRecursive(t, root, relPath, out)
		} else {
			data, err := os.ReadFile(filepath.Join(root, relPath))
			if err != nil {
				t.Fatal(err)
			}
			out[relPath] = string(data)
		}
	}
}

// TestMergeTableObjectsGolden verifies MergeTableObjects output against a
// known-good fixture. Any change in TABLE file bytes, surviving file set,
// or index.txt content (except N1 reordering) is a regression.
func TestMergeTableObjectsGolden(t *testing.T) {
	destDir, before := buildGoldenFixture(t)

	if err := MergeTableObjects(destDir); err != nil {
		t.Fatalf("MergeTableObjects failed: %v", err)
	}

	// Expected TABLE file contents after merge (exact bytes).
	// Merge order per table: DEFAULT → CONSTRAINT → FK_CONSTRAINT → INDEX → TRIGGER.
	// inlineSequences converts users.id to bigserial before merge.
	wantUsers := "CREATE TABLE public.users (\n    id bigserial NOT NULL,\n    name text\n);\n\n" +
		"ALTER TABLE public.users ADD CONSTRAINT users_name_not_empty CHECK (name <> '');\n\n" +
		"CREATE INDEX users_name_idx ON users(name);\n\n" +
		"CREATE TRIGGER users_audit AFTER UPDATE ON users FOR EACH ROW EXECUTE PROCEDURE audit();\n"

	wantOrders := before["public/TABLE/orders.sql"] +
		"\n" + before["public/DEFAULT/orders.sql"] + "\n" +
		before["public/FK_CONSTRAINT/orders.sql"] + "\n" +
		before["public/INDEX/orders_user_id_idx.sql"]

	// items: CYCLE sequence is NOT inlined; DEFAULT is merged into TABLE.
	wantItems := before["public/TABLE/items.sql"] +
		"\n" + before["public/DEFAULT/items.sql"] + "\n" +
		before["public/INDEX/items_name_idx.sql"]

	// Verify exact TABLE file bytes
	for wantPath, wantContent := range map[string]string{
		"public/TABLE/users.sql":  wantUsers,
		"public/TABLE/orders.sql": wantOrders,
		"public/TABLE/items.sql":  wantItems,
	} {
		got, err := os.ReadFile(filepath.Join(destDir, wantPath))
		if err != nil {
			t.Fatalf("read %s: %v", wantPath, err)
		}
		if string(got) != wantContent {
			t.Errorf("%s mismatch:\ngot:\n%s\nwant:\n%s", wantPath, string(got), wantContent)
		}
	}

	// Merged files should be removed
	removed := []string{
		"public/INDEX/users.sql",
		"public/INDEX/orders_user_id_idx.sql",
		"public/INDEX/items_name_idx.sql",
		"public/TRIGGER/users.sql",
		"public/DEFAULT/orders.sql",
		"public/DEFAULT/items.sql",
		"public/FK_CONSTRAINT/orders.sql",
		"public/CONSTRAINT/users.sql",
	}
	for _, relPath := range removed {
		if _, err := os.Stat(filepath.Join(destDir, relPath)); !os.IsNotExist(err) {
			t.Errorf("merged file %s should be removed", relPath)
		}
	}

	// Unresolvable index should survive
	if _, err := os.Stat(filepath.Join(destDir, "public/INDEX/unresolvable_parent_idx.sql")); os.IsNotExist(err) {
		t.Error("unresolvable index should survive unmerged")
	}

	// Inlineable sequence should be removed (inlined as bigserial)
	if _, err := os.Stat(filepath.Join(destDir, "public/SEQUENCE/users_id_seq.sql")); !os.IsNotExist(err) {
		t.Error("inlineable SEQUENCE should be removed")
	}

	// Non-inlineable (CYCLE) sequence should survive
	if _, err := os.Stat(filepath.Join(destDir, "public/SEQUENCE/items_id_seq.sql")); os.IsNotExist(err) {
		t.Error("non-inlineable SEQUENCE should survive")
	}

	// users TABLE should have bigserial (inlined)
	usersData, _ := os.ReadFile(filepath.Join(destDir, "public/TABLE/users.sql"))
	if !strings.Contains(string(usersData), "bigserial") {
		t.Errorf("users.sql should contain bigserial after inlining")
	}

	// items TABLE should NOT have bigserial (CYCLE sequence not inlined)
	itemsData, _ := os.ReadFile(filepath.Join(destDir, "public/TABLE/items.sql"))
	if strings.Contains(string(itemsData), "bigserial") {
		t.Errorf("items.sql should NOT contain bigserial (CYCLE sequence)")
	}

	// Verify index.txt: pruned entries, all remaining entries exist on disk
	idxData, err := os.ReadFile(filepath.Join(destDir, "index.txt"))
	if err != nil {
		t.Fatal(err)
	}
	idxLines := strings.Split(strings.TrimSuffix(string(idxData), "\n"), "\n")
	for _, line := range idxLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(destDir, line)); os.IsNotExist(err) {
			t.Errorf("index.txt entry %q does not exist on disk", line)
		}
	}

	// Verify merged entries are removed from index.txt
	for _, notWant := range []string{
		"public/INDEX/users.sql",
		"public/INDEX/orders_user_id_idx.sql",
		"public/INDEX/items_name_idx.sql",
		"public/TRIGGER/users.sql",
		"public/DEFAULT/orders.sql",
		"public/DEFAULT/items.sql",
		"public/FK_CONSTRAINT/orders.sql",
		"public/CONSTRAINT/users.sql",
		"public/SEQUENCE/users_id_seq.sql", // inlined
	} {
		if strings.Contains(string(idxData), notWant) {
			t.Errorf("index.txt should NOT contain %q", notWant)
		}
	}

	// N1: SEQUENCE should appear before referencing TABLE in index.txt.
	// items.sql references items_id_seq via nextval.
	idxStr := string(idxData)
	itemsIdx := strings.Index(idxStr, "public/TABLE/items.sql")
	itemsSeqIdx := strings.Index(idxStr, "public/SEQUENCE/items_id_seq.sql")
	if itemsIdx == -1 {
		t.Error("index.txt should contain public/TABLE/items.sql")
	}
	if itemsSeqIdx == -1 {
		t.Error("index.txt should contain public/SEQUENCE/items_id_seq.sql")
	}
	if itemsSeqIdx > itemsIdx {
		t.Errorf("SEQUENCE should appear before TABLE in index.txt (N1): items_seq at %d, items at %d", itemsSeqIdx, itemsIdx)
	}
}

// TestMergeTableObjectsN1Replay verifies that a reordered index.txt (N1 fix)
// can be consumed by merge.FromIndex and produces a valid merged SQL file where
// the CREATE SEQUENCE statement precedes the nextval DEFAULT reference.
func TestMergeTableObjectsN1Replay(t *testing.T) {
	destDir := t.TempDir()

	for _, d := range []string{"public/TABLE", "public/SEQUENCE"} {
		if err := os.MkdirAll(filepath.Join(destDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Table with a non-inlineable sequence (CYCLE).
	tableSQL := strings.Join([]string{
		"CREATE TABLE public.items (",
		"    id integer NOT NULL,",
		"    name text",
		");",
		"",
		"ALTER TABLE public.items ALTER COLUMN id SET DEFAULT nextval('items_id_seq');",
		"",
	}, "\n")
	seqSQL := strings.Join([]string{
		"CREATE SEQUENCE public.items_id_seq",
		"    START WITH 0",
		"    INCREMENT BY 1",
		"    MINVALUE 0",
		"    MAXVALUE 999999",
		"    CACHE 1",
		"    CYCLE;",
		"",
	}, "\n")

	files := map[string]string{
		"public/TABLE/items.sql": tableSQL,
		"public/SEQUENCE/items_id_seq.sql": seqSQL,
		"index.txt": strings.Join([]string{
			"public/TABLE/items.sql",
			"public/SEQUENCE/items_id_seq.sql",
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

	// Verify SEQUENCE precedes TABLE in index.txt (N1).
	idxData, err := os.ReadFile(filepath.Join(destDir, "index.txt"))
	if err != nil {
		t.Fatal(err)
	}
	idxStr := string(idxData)
	seqIdx := strings.Index(idxStr, "public/SEQUENCE/items_id_seq.sql")
	tableIdx := strings.Index(idxStr, "public/TABLE/items.sql")
	if seqIdx == -1 {
		t.Fatal("index.txt missing SEQUENCE entry")
	}
	if tableIdx == -1 {
		t.Fatal("index.txt missing TABLE entry")
	}
	if seqIdx > tableIdx {
		t.Errorf("SEQUENCE should precede TABLE in index.txt: seq=%d, table=%d", seqIdx, tableIdx)
	}

	// Verify merge.FromIndex can consume the reordered index.
	outputFile := filepath.Join(destDir, "output.sql")
	if err := merge.FromIndex(filepath.Join(destDir, "index.txt"), outputFile); err != nil {
		t.Fatalf("FromIndex failed: %v", err)
	}

	mergedData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	merged := string(mergedData)

	// Verify CREATE SEQUENCE appears before nextval DEFAULT in merged output.
	createSeqIdx := strings.Index(merged, "CREATE SEQUENCE public.items_id_seq")
	nextvalIdx := strings.Index(merged, "nextval('items_id_seq')")
	if createSeqIdx == -1 {
		t.Error("merged output missing CREATE SEQUENCE")
	}
	if nextvalIdx == -1 {
		t.Error("merged output missing nextval DEFAULT")
	}
	if nextvalIdx < createSeqIdx {
		t.Errorf("CREATE SEQUENCE should precede nextval in merged output: createSeq=%d, nextval=%d", createSeqIdx, nextvalIdx)
	}
}

// BenchmarkMergeTableObjects measures the performance of MergeTableObjects
// on a synthetic schema with many tables and child files.
func BenchmarkMergeTableObjects(b *testing.B) {
	destDir := b.TempDir()
	schemaDir := filepath.Join(destDir, "public")

	// Create directories
	for _, d := range []string{"TABLE", "INDEX", "TRIGGER", "DEFAULT", "FK_CONSTRAINT", "CONSTRAINT"} {
		if err := os.MkdirAll(filepath.Join(schemaDir, d), 0o755); err != nil {
			b.Fatal(err)
		}
	}

	const numTables = 300
	const childrenPerTable = 3 // INDEX + TRIGGER + DEFAULT

	var indexEntries []string

	for i := 0; i < numTables; i++ {
		tableName := fmt.Sprintf("t%04d", i)
		// TABLE file
		tableContent := fmt.Sprintf("CREATE TABLE public.%s (id integer NOT NULL);\n", tableName)
		tablePath := filepath.Join(schemaDir, "TABLE", tableName+".sql")
		if err := os.WriteFile(tablePath, []byte(tableContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/TABLE/"+tableName+".sql")

		// INDEX file (named after index, references table via ON clause)
		idxContent := fmt.Sprintf("CREATE INDEX %s_idx ON public.%s(id);\n", tableName, tableName)
		idxPath := filepath.Join(schemaDir, "INDEX", tableName+"_idx.sql")
		if err := os.WriteFile(idxPath, []byte(idxContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/INDEX/"+tableName+"_idx.sql")

		// TRIGGER file (named after trigger, references table via ON clause)
		trigContent := fmt.Sprintf("CREATE TRIGGER %s_audit AFTER UPDATE ON public.%s FOR EACH ROW EXECUTE PROCEDURE audit();\n", tableName, tableName)
		trigPath := filepath.Join(schemaDir, "TRIGGER", tableName+".sql")
		if err := os.WriteFile(trigPath, []byte(trigContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/TRIGGER/"+tableName+".sql")

		// DEFAULT file (named after table, filename-based merge)
		defContent := fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN id SET DEFAULT nextval('%s_id_seq');\n", tableName, tableName)
		defPath := filepath.Join(schemaDir, "DEFAULT", tableName+".sql")
		if err := os.WriteFile(defPath, []byte(defContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/DEFAULT/"+tableName+".sql")

		// FK_CONSTRAINT file (named after table, filename-based merge)
		fkContent := fmt.Sprintf("ALTER TABLE public.%s ADD CONSTRAINT %s_fk FOREIGN KEY (id) REFERENCES other(id);\n", tableName, tableName)
		fkPath := filepath.Join(schemaDir, "FK_CONSTRAINT", tableName+".sql")
		if err := os.WriteFile(fkPath, []byte(fkContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/FK_CONSTRAINT/"+tableName+".sql")

		// CONSTRAINT file (named after table, filename-based merge)
		cstContent := fmt.Sprintf("ALTER TABLE public.%s ADD CONSTRAINT %s_chk CHECK (id > 0);\n", tableName, tableName)
		cstPath := filepath.Join(schemaDir, "CONSTRAINT", tableName+".sql")
		if err := os.WriteFile(cstPath, []byte(cstContent), 0o644); err != nil {
			b.Fatal(err)
		}
		indexEntries = append(indexEntries, "public/CONSTRAINT/"+tableName+".sql")
	}

	// Write index.txt
	sort.Strings(indexEntries)
	indexContent := strings.Join(indexEntries, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(destDir, "index.txt"), []byte(indexContent), 0o644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Re-create the directory contents for each iteration
		for j := 0; j < numTables; j++ {
			tableName := fmt.Sprintf("t%04d", j)
			tableContent := fmt.Sprintf("CREATE TABLE public.%s (id integer NOT NULL);\n", tableName)
			os.WriteFile(filepath.Join(schemaDir, "TABLE", tableName+".sql"), []byte(tableContent), 0o644)
			idxContent := fmt.Sprintf("CREATE INDEX %s_idx ON public.%s(id);\n", tableName, tableName)
			os.WriteFile(filepath.Join(schemaDir, "INDEX", tableName+"_idx.sql"), []byte(idxContent), 0o644)
			trigContent := fmt.Sprintf("CREATE TRIGGER %s_audit AFTER UPDATE ON public.%s FOR EACH ROW EXECUTE PROCEDURE audit();\n", tableName, tableName)
			os.WriteFile(filepath.Join(schemaDir, "TRIGGER", tableName+".sql"), []byte(trigContent), 0o644)
			defContent := fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN id SET DEFAULT nextval('%s_id_seq');\n", tableName, tableName)
			os.WriteFile(filepath.Join(schemaDir, "DEFAULT", tableName+".sql"), []byte(defContent), 0o644)
			fkContent := fmt.Sprintf("ALTER TABLE public.%s ADD CONSTRAINT %s_fk FOREIGN KEY (id) REFERENCES other(id);\n", tableName, tableName)
			os.WriteFile(filepath.Join(schemaDir, "FK_CONSTRAINT", tableName+".sql"), []byte(fkContent), 0o644)
			cstContent := fmt.Sprintf("ALTER TABLE public.%s ADD CONSTRAINT %s_chk CHECK (id > 0);\n", tableName, tableName)
			os.WriteFile(filepath.Join(schemaDir, "CONSTRAINT", tableName+".sql"), []byte(cstContent), 0o644)
		}
		b.StartTimer()
		if err := MergeTableObjects(destDir); err != nil {
			b.Fatalf("MergeTableObjects failed: %v", err)
		}
	}
}
