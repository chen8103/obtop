package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chen8103/obtop"
	"github.com/chen8103/obtop/obmodel"
)

func TestEncodeSnapshotPreservesLongMultilineSQL(t *testing.T) {
	longSQL := "SELECT '" + strings.Repeat("你好", 12000) + "'\nFROM t\nWHERE id = 1"
	if utf8.RuneCountInString(longSQL) <= 20000 {
		// ensure we are above TUI display limit for Chinese chars
		longSQL = longSQL + strings.Repeat("x", 5000)
	}
	snap := obtop.Snapshot{
		Filtered: []obmodel.Process{{
			SessID:  9,
			Command: "Query",
			SQL:     longSQL,
		}},
		SampledAt: time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC),
		SQLRates:  obmodel.SQLRates{Ready: true, Values: map[int64]float64{40000: 1}},
	}
	rec := encodeSnapshotRecord(1, snap)
	var buf bytes.Buffer
	if err := writeJSON(&buf, rec); err != nil {
		t.Fatal(err)
	}
	line := buf.String()
	if strings.Contains(line, "\x1b") {
		t.Fatal("stdout payload must not contain ANSI")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	sessions := decoded["sessions"].([]interface{})
	gotSQL := sessions[0].(map[string]interface{})["sql"].(string)
	if gotSQL != longSQL {
		t.Fatalf("sql round-trip lost data: got %d runes want %d", utf8.RuneCountInString(gotSQL), utf8.RuneCountInString(longSQL))
	}
}

func TestEncodeSnapshotDistinguishesSessionErrorFromEmpty(t *testing.T) {
	emptyOK := encodeSnapshotRecord(1, obtop.Snapshot{
		SampledAt: time.Now(),
		Filtered:  []obmodel.Process{},
	})
	if emptyOK["error"] != nil {
		t.Fatalf("empty success must have null error, got %#v", emptyOK["error"])
	}
	failed := encodeSnapshotRecord(2, obtop.Snapshot{
		SampledAt:   time.Now(),
		SessionsErr: errString("sessions down"),
	})
	if failed["error"] == nil {
		t.Fatal("session failure must encode error field")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestEncodeHistoryIncludesSourceID(t *testing.T) {
	rec := encodeHistoryRecord(1, obtop.HistoryRecord{
		SourceID:  "10.0.0.1:2882/7",
		RequestID: 7,
		SQL:       "select 1",
	})
	if rec["source_id"] != "10.0.0.1:2882/7" {
		t.Fatalf("source_id missing: %#v", rec)
	}
}

func TestEncodeHistoryMetaIncludesHasMore(t *testing.T) {
	page := obtop.HistoryPage{
		Completeness: obtop.CompletenessComplete,
		HasMore:      true,
		NextCursor:   &obtop.HistoryCursor{ServerIP: "10.0.0.1", ServerPort: 2882, RequestID: 9},
		Records:      []obmodel.HistoryRecord{{SourceID: "10.0.0.1:2882/9"}},
	}
	meta := encodeHistoryMeta(page)
	if meta["has_more"] != true {
		t.Fatalf("has_more missing: %#v", meta)
	}
	if meta["next_cursor"] != "10.0.0.1:2882/9" {
		t.Fatalf("next_cursor = %#v", meta["next_cursor"])
	}
	// Retention complete + export truncated must be distinguishable.
	if meta["completeness"] != obtop.CompletenessComplete {
		t.Fatalf("completeness should stay complete, got %#v", meta["completeness"])
	}
}

func TestParseHistoryCursor(t *testing.T) {
	cursor, err := parseHistoryCursor("10.0.0.1:2882/42")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ServerIP != "10.0.0.1" || cursor.ServerPort != 2882 || cursor.RequestID != 42 {
		t.Fatalf("unexpected %#v", cursor)
	}
	if _, err := parseHistoryCursor("bad"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestMergeWorstCompletenessAcrossPages(t *testing.T) {
	got, reason := mergeWorstCompleteness(
		obtop.CompletenessUnknown, "enable unknown",
		obtop.CompletenessComplete, "",
	)
	if got != obtop.CompletenessUnknown || reason != "enable unknown" {
		t.Fatalf("later complete must not override earlier unknown: %s %q", got, reason)
	}

	got, reason = mergeWorstCompleteness(
		obtop.CompletenessComplete, "",
		obtop.CompletenessKnownGap, "purged",
	)
	if got != obtop.CompletenessKnownGap || reason != "purged" {
		t.Fatalf("known_gap must win: %s %q", got, reason)
	}
}

func TestParseSinceDuration(t *testing.T) {
	got, err := parseSince("5m")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(got) < 4*time.Minute || time.Since(got) > 6*time.Minute {
		t.Fatalf("unexpected since %s", got)
	}
}
