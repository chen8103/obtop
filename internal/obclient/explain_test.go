package obclient

import "testing"

func TestReplacePlaceholdersSkipsLiteralsAndComments(t *testing.T) {
	input := `SELECT id FROM t WHERE a = ? AND b = '?' AND c = "?" /* ? */ -- ?
AND d = ? AND e = 'it''s ? weird'`
	got, replaced := replacePlaceholdersOutsideLiterals(input)
	if replaced != 2 {
		t.Fatalf("replaced = %d, want 2", replaced)
	}
	if !containsAll(got, []string{"a = NULL", "b = '?'", `c = "?"`, "/* ? */", "-- ?", "d = NULL", "'it''s ? weird'"}) {
		t.Fatalf("unexpected rewrite:\n%s", got)
	}
}

func TestPrepareExplainSQLRejectsUnsupported(t *testing.T) {
	if _, _, err := prepareExplainSQL("SHOW TABLES"); err == nil {
		t.Fatal("expected unsupported statement error")
	}
}

func TestPrepareExplainSQLNotesReplacement(t *testing.T) {
	sqlText, notes, err := prepareExplainSQL("SELECT * FROM t WHERE id = ?")
	if err != nil {
		t.Fatal(err)
	}
	if sqlText != "SELECT * FROM t WHERE id = NULL" {
		t.Fatalf("sql = %q", sqlText)
	}
	if len(notes) == 0 {
		t.Fatal("expected replacement note")
	}
}

func containsAll(haystack string, needles []string) bool {
	for _, needle := range needles {
		if !containsString(haystack, needle) {
			return false
		}
	}
	return true
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})())
}
