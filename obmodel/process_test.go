package obmodel

import "testing"

func TestProcessIdentifiesProxySession(t *testing.T) {
	proxy := Process{SessID: 101, ProxySessID: 9001}
	direct := Process{SessID: 102}

	if proxy.ProxySessID <= 0 {
		t.Fatalf("proxy session id = %d, want positive", proxy.ProxySessID)
	}
	if direct.ProxySessID != 0 {
		t.Fatalf("direct proxy session id = %d, want zero", direct.ProxySessID)
	}
}

func TestSessionStatsByUserSeparatesActiveAndInactiveSessions(t *testing.T) {
	rows := []Process{
		{User: "app", Command: "Query"},
		{User: "app", Command: "Sleep"},
		{User: "report", Command: "SLEEP"},
		{User: "report", Command: "Query"},
		{User: "report", Command: "Execute"},
		{User: "", Command: "Sleep"},
	}

	got := SessionStatsByUser(rows)
	want := []UserSessionStats{
		{User: "(unknown)", Active: 0, Inactive: 1},
		{User: "app", Active: 1, Inactive: 1},
		{User: "report", Active: 2, Inactive: 1},
	}

	if len(got) != len(want) {
		t.Fatalf("SessionStatsByUser() returned %d rows, want %d", len(got), len(want))
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("SessionStatsByUser()[%d] = %#v, want %#v", idx, got[idx], want[idx])
		}
	}
}

func TestActiveProcessesExcludesSleepSessions(t *testing.T) {
	rows := []Process{
		{SessID: 1, Command: "Sleep"},
		{SessID: 2, Command: "Query"},
		{SessID: 3, Command: " sleep "},
	}

	got := ActiveProcesses(rows)
	if len(got) != 1 || got[0].SessID != 2 {
		t.Fatalf("ActiveProcesses() = %#v, want only session 2", got)
	}
}

func TestKillSessionIDsByUserUsesProcesslistIDs(t *testing.T) {
	rows := []Process{
		{SessID: 101, User: "app"},
		{SessID: 101, User: "app"},
		{SessID: 103, User: "app"},
		{SessID: 104, User: "app_readonly"},
	}

	got := KillSessionIDsByUser(rows, "app")
	want := []int64{101, 103}
	if len(got) != len(want) {
		t.Fatalf("KillSessionIDsByUser() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("KillSessionIDsByUser()[%d] = %d, want %d", index, got[index], want[index])
		}
	}
}
