package diff

import (
	"strings"
	"testing"
)

const samplePatch = `@@ -8,6 +8,9 @@ func get_user(id int) {
 	ctx := context.Background()
-	row := db.QueryRow("SELECT name FROM users WHERE id = $1", id)
-	return row.Scan(&name)
+	query := "SELECT name FROM users WHERE id = " + strconv.Itoa(id)
+	row := db.QueryRow(query)
+	var name string
+	return row.Scan(&name)
 }
 
 func deleteUser(id int) {`

func TestParseFilePatchLineNumbers(t *testing.T) {
	f, err := ParseFilePatch("api/users.go", "modified", samplePatch)
	if err != nil {
		t.Fatalf("ParseFilePatch: %v", err)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 8 || h.NewStart != 8 {
		t.Fatalf("starts = %d/%d, want 8/8", h.OldStart, h.NewStart)
	}
	if h.OldCount != 6 || h.NewCount != 9 {
		t.Fatalf("counts = %d/%d, want 6/9", h.OldCount, h.NewCount)
	}

	added := f.AddedLineNumbers()
	for _, n := range []int{9, 10, 11, 12} {
		if _, ok := added[n]; !ok {
			t.Errorf("line %d not marked as added", n)
		}
	}
	if _, ok := added[8]; ok {
		t.Errorf("context line 8 marked as added")
	}
	if f.Additions != 4 || f.Deletions != 2 {
		t.Errorf("additions/deletions = %d/%d, want 4/2", f.Additions, f.Deletions)
	}

	line, ok := f.ChangedLine(10)
	if !ok || line.Kind != Added || !strings.Contains(line.Content, "QueryRow(query)") {
		t.Errorf("ChangedLine(10) = %+v, %v", line, ok)
	}
}

func TestParseFilePatchDefaultsCountWhenOmitted(t *testing.T) {
	f, err := ParseFilePatch("main.go", "modified", "@@ -1 +1 @@\n-old\n+new\n")
	if err != nil {
		t.Fatal(err)
	}
	h := f.Hunks[0]
	if h.OldCount != 1 || h.NewCount != 1 {
		t.Errorf("counts = %d/%d, want 1/1", h.OldCount, h.NewCount)
	}
}

func TestParseFilePatchRenamesAndBinary(t *testing.T) {
	patch := "diff --git a/old/name.go b/new/name.go\n" +
		"similarity index 90%\n" +
		"rename from old/name.go\n" +
		"rename to new/name.go\n" +
		"@@ -1,2 +1,2 @@\n a\n-b\n+c\n"
	f, err := ParseFilePatch("", "", patch)
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != "new/name.go" || f.OldPath != "old/name.go" || f.Status != "renamed" {
		t.Errorf("got path=%q old=%q status=%q", f.Path, f.OldPath, f.Status)
	}

	bin, err := ParseFilePatch("logo.png", "modified", "Binary files a/logo.png and b/logo.png differ\n")
	if err != nil {
		t.Fatal(err)
	}
	if !bin.Binary {
		t.Error("binary file not detected")
	}
}

func TestParseFilePatchRequiresPath(t *testing.T) {
	if _, err := ParseFilePatch("", "", "@@ -1 +1 @@\n-a\n+b\n"); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestAnchorLineFindsNearestAddedLine(t *testing.T) {
	f, err := ParseFilePatch("api/users.go", "modified", samplePatch)
	if err != nil {
		t.Fatal(err)
	}
	// line 8 is context; nearest added line is 9
	if got, ok := f.AnchorLine(8); !ok || got != 9 {
		t.Errorf("AnchorLine(8) = %d, %v; want 9, true", got, ok)
	}
	if got, ok := f.AnchorLine(10); !ok || got != 10 {
		t.Errorf("AnchorLine(10) = %d, %v; want 10, true", got, ok)
	}
	if _, ok := f.AnchorLine(900); ok {
		t.Error("line outside every hunk should not anchor")
	}
}

func TestChunkFileSplitsByBudgetAndKeepsLineNumbers(t *testing.T) {
	var b strings.Builder
	b.WriteString("@@ -1,1 +1,1 @@\n")
	for i := 0; i < 200; i++ {
		b.WriteString("+line " + strings.Repeat("x", 40) + "\n")
	}
	f, err := ParseFilePatch("big.go", "modified", b.String())
	if err != nil {
		t.Fatal(err)
	}
	chunks := ChunkFile(f, 2000)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want >= 2", len(chunks))
	}

	seen := map[int]bool{}
	for _, c := range chunks {
		if c.Chars > 2000 && len(c.Hunks) > 1 {
			t.Errorf("chunk over budget: %d chars", c.Chars)
		}
		for _, h := range c.Hunks {
			for _, l := range h.Lines {
				if l.Kind == Added {
					seen[l.NewNumber] = true
				}
			}
		}
	}
	if len(seen) != 200 {
		t.Errorf("saw %d added lines across chunks, want 200", len(seen))
	}
}

func TestRenderHunksRoundTrip(t *testing.T) {
	f, err := ParseFilePatch("api/users.go", "modified", samplePatch)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderHunks(f, f.Hunks)
	if !strings.Contains(out, "### api/users.go (+4/-2)") {
		t.Errorf("missing file header:\n%s", out)
	}
	if !strings.Contains(out, "+	query := ") {
		t.Errorf("missing added line:\n%s", out)
	}
}

func FuzzParseFilePatch(f *testing.F) {
	f.Add(samplePatch)
	f.Add("")
	f.Add("@@ -1 +1 @@\n")
	f.Fuzz(func(t *testing.T, patch string) {
		file, err := ParseFilePatch("x.go", "modified", patch)
		if err != nil {
			return
		}
		// Invariant: every reported added line is reachable by ChangedLine.
		for n := range file.AddedLineNumbers() {
			if _, ok := file.ChangedLine(n); !ok {
				t.Fatalf("added line %d not reachable", n)
			}
		}
	})
}
