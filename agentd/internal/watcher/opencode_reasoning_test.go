package watcher

import (
	"testing"
)

func TestOpenCodeDBHistoryWithReasoning(t *testing.T) {
	dbPath := FindOpenCodeDB()
	if dbPath == "" {
		t.Skip("No opencode DB found")
	}
	
	sessionID := "ses_1fd655113ffeiSdqZldHDmO3gj"
	
	events, err := OpenCodeDBHistory(sessionID)
	if err != nil {
		t.Fatalf("OpenCodeDBHistory failed: %v", err)
	}
	
	t.Logf("Total events: %d", len(events))
	if len(events) == 0 {
		t.Skip("No events found for this session")
	}
}
