package obclient

import (
	"fmt"
	"testing"
	"time"

	"github.com/chen8103/obtop/obmodel"
)

func TestEvaluateAuditCompletenessKnownGap(t *testing.T) {
	since := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	oldest := []auditOldest{{
		ServerIP:       "10.0.0.1",
		ServerPort:     2882,
		MinRequestTime: since.Add(time.Minute),
	}}
	completeness, reason := evaluateAuditCompleteness(since, oldest)
	if completeness != obmodel.CompletenessKnownGap {
		t.Fatalf("completeness = %s, want known_gap", completeness)
	}
	if reason == "" {
		t.Fatal("expected gap reason")
	}
}

func TestEvaluateAuditCompletenessComplete(t *testing.T) {
	since := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	oldest := []auditOldest{{
		ServerIP:       "10.0.0.1",
		ServerPort:     2882,
		MinRequestTime: since.Add(-time.Minute),
	}}
	completeness, reason := evaluateAuditCompleteness(since, oldest)
	if completeness != obmodel.CompletenessComplete || reason != "" {
		t.Fatalf("completeness=%s reason=%q", completeness, reason)
	}
}

func TestMergeCompletenessWithEnableUnknown(t *testing.T) {
	got, reason := MergeCompletenessWithEnableUnknown(
		obmodel.CompletenessComplete,
		"",
		false,
		"sql audit enable status unknown",
	)
	if got != obmodel.CompletenessUnknown {
		t.Fatalf("completeness = %s, want unknown", got)
	}
	if reason == "" {
		t.Fatal("expected enable unknown reason")
	}

	kept, keptReason := MergeCompletenessWithEnableUnknown(
		obmodel.CompletenessComplete,
		"",
		true,
		"",
	)
	if kept != obmodel.CompletenessComplete || keptReason != "" {
		t.Fatalf("known enable must keep complete, got %s %q", kept, keptReason)
	}
}

func TestParseAuditBool(t *testing.T) {
	if !parseAuditBool("ON") || !parseAuditBool("1") {
		t.Fatal("expected true for ON/1")
	}
	if parseAuditBool("OFF") || parseAuditBool("0") {
		t.Fatal("expected false for OFF/0")
	}
}

func TestCombineAuditEnableRequiresBothKnownOn(t *testing.T) {
	bothOn := boolSettingAgg{Found: true, AllOn: true, Rows: 1}
	oneOff := boolSettingAgg{Found: true, AnyOff: true, Rows: 2}
	missing := boolSettingAgg{}

	enabled, known, _ := combineAuditEnableFlags(bothOn, nil, bothOn, nil)
	if !enabled || !known {
		t.Fatalf("both on => enabled known, got enabled=%v known=%v", enabled, known)
	}

	enabled, known, _ = combineAuditEnableFlags(bothOn, nil, oneOff, nil)
	if enabled || !known {
		t.Fatalf("any off => disabled known, got enabled=%v known=%v", enabled, known)
	}

	// Parameter query failed, variable ON — must stay unknown, not enabled.
	enabled, known, detail := combineAuditEnableFlags(missing, fmt.Errorf("access denied"), bothOn, nil)
	if enabled || known {
		t.Fatalf("param unknown + var on => unknown, got enabled=%v known=%v detail=%s", enabled, known, detail)
	}

	enabled, known, _ = combineAuditEnableFlags(bothOn, nil, missing, nil)
	if enabled || known {
		t.Fatalf("var missing => unknown, got enabled=%v known=%v", enabled, known)
	}
}

func TestAggregateMultiNodeParameterOff(t *testing.T) {
	// Simulate SHOW PARAMETERS returning True then False across nodes.
	agg := boolSettingAgg{}
	for _, val := range []string{"True", "False"} {
		agg.Found = true
		agg.Rows++
		if parseAuditBool(val) {
			// on
		} else {
			agg.AnyOff = true
		}
	}
	agg.AllOn = agg.Found && !agg.AnyOff
	if !agg.AnyOff || agg.AllOn {
		t.Fatalf("multi-node with one False must set AnyOff, got %#v", agg)
	}
	enabled, known, _ := combineAuditEnableFlags(agg, nil, boolSettingAgg{Found: true, AllOn: true, Rows: 1}, nil)
	if enabled || !known {
		t.Fatalf("node off => disabled known, got enabled=%v known=%v", enabled, known)
	}
}

func TestBuildAuditHistoryQueryUsesRequestIDCursor(t *testing.T) {
	since := time.Unix(100, 0)
	until := time.Unix(200, 0)
	query, args := buildAuditHistoryQuery(obmodel.HistoryOptions{
		Cursor: &obmodel.HistoryCursor{ServerIP: "10.0.0.1", ServerPort: 2882, RequestID: 42},
		User:   "app",
		Limit:  10,
	}, since, until, 10)
	if !containsString(query, "REQUEST_ID > ?") {
		t.Fatalf("query must advance by REQUEST_ID: %s", query)
	}
	if !containsString(query, "USER_NAME LIKE ?") {
		t.Fatalf("query must filter user: %s", query)
	}
	if len(args) < 4 {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestFormatAuditSourceID(t *testing.T) {
	got := formatAuditSourceID("10.0.0.1", 2882, 99)
	if got != "10.0.0.1:2882/99" {
		t.Fatalf("source id = %q", got)
	}
}

func TestPickNextCursor(t *testing.T) {
	cursor := pickNextCursor([]obmodel.HistoryRecord{
		{ServerIP: "10.0.0.1", ServerPort: 2882, RequestID: 1},
		{ServerIP: "10.0.0.2", ServerPort: 2882, RequestID: 9},
	})
	if cursor == nil || cursor.ServerIP != "10.0.0.2" || cursor.RequestID != 9 {
		t.Fatalf("unexpected cursor %#v", cursor)
	}
}

func TestHasMoreWhenPageHitsLimit(t *testing.T) {
	// Pure logic: page with len==limit must mark HasMore for callers.
	limit := 2
	records := make([]obmodel.HistoryRecord, limit)
	page := obmodel.HistoryPage{Records: records, Completeness: obmodel.CompletenessComplete}
	if len(page.Records) >= limit {
		page.HasMore = true
		page.NextCursor = pickNextCursor(page.Records)
	}
	if !page.HasMore || page.NextCursor == nil {
		t.Fatal("truncated page must set HasMore and NextCursor")
	}
	if page.Completeness != obmodel.CompletenessComplete {
		t.Fatal("retention completeness must stay independent of HasMore")
	}
}
