package obclient

import (
	"strings"
	"testing"
)

func TestActiveSQLQuerySelectsProxySessionIDForDrainTracking(t *testing.T) {
	if !strings.Contains(activeSQLQuery, "PROXY_SESSID") {
		t.Fatalf("active process query must select PROXY_SESSID for proxy-aware drain tracking: %s", activeSQLQuery)
	}
}
