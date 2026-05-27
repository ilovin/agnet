package agent

import (
	"log"
	"time"

	"github.com/phone-talk/agentd/internal/eventbuf"
	"github.com/phone-talk/agentd/internal/store"
)

// EventManager handles event persistence and querying for agents.
type EventManager struct {
	store *store.Store
}

// NewEventManager creates a new EventManager.
func NewEventManager(s *store.Store) *EventManager {
	return &EventManager{store: s}
}

// AppendAndPersistEvent appends an event to the agent's buffer and persists it.
func (em *EventManager) AppendAndPersistEvent(agentID string, ag *Agent, data map[string]any) uint64 {
	if _, ok := data["timestamp"]; !ok {
		data["timestamp"] = time.Now().UnixMilli()
	}
	seq := ag.AppendEvent(data)
	sessionID := ag.ResumeSessionID()
	if err := em.store.SaveConversationEventWithSession(agentID, sessionID, seq, data); err != nil {
		log.Printf("save conversation event agent=%s seq=%d: %v", agentID, seq, err)
	}
	return seq
}

// UpdateOrAppendEvent handles streaming message updates: if data contains a
// msg_id matching an existing event, it updates in place (buffer + store) and
// returns (existingSeq, true). Otherwise it appends a new event.
func (em *EventManager) UpdateOrAppendEvent(agentID string, ag *Agent, data map[string]any) (uint64, bool) {
	msgID, _ := data["msg_id"].(string)
	seq, updated := ag.EventBuf().UpdateOrAppend(msgID, data)
	sessionID := ag.ResumeSessionID()
	if updated {
		data["seq"] = seq
		if err := em.store.SaveConversationEventWithSession(agentID, sessionID, seq, data); err != nil {
			log.Printf("update conversation event agent=%s seq=%d: %v", agentID, seq, err)
		}
	} else {
		if err := em.store.SaveConversationEventWithSession(agentID, sessionID, seq, data); err != nil {
			log.Printf("save conversation event agent=%s seq=%d: %v", agentID, seq, err)
		}
	}
	return seq, updated
}

// RecordConversationEvent records a conversation event for an agent.
func (em *EventManager) RecordConversationEvent(agentID string, ag *Agent, data map[string]any) (uint64, error) {
	return em.AppendAndPersistEvent(agentID, ag, data), nil
}

// LoadPersistedEventsLatest loads the latest persisted events for an agent.
func (em *EventManager) LoadPersistedEventsLatest(agentID string, limit int) ([]eventbuf.Event, error) {
	records, err := em.store.ListConversationEventsLatest(agentID, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role":      r.Role,
			"text":      r.Text,
			"raw":       r.Raw,
			"kind":      r.Kind,
			"timestamp": parseEventRowTimestamp(r.CreatedAt),
		}
		for k, v := range r.Payload {
			data[k] = v
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

// LoadPersistedEventsSince loads persisted events after a given sequence.
func (em *EventManager) LoadPersistedEventsSince(agentID string, afterSeq uint64, limit int) ([]eventbuf.Event, error) {
	records, err := em.store.ListConversationEventsSince(agentID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role":      r.Role,
			"text":      r.Text,
			"raw":       r.Raw,
			"kind":      r.Kind,
			"timestamp": parseEventRowTimestamp(r.CreatedAt),
		}
		for k, v := range r.Payload {
			data[k] = v
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

// LoadPersistedEventsBefore loads persisted events before a given sequence.
func (em *EventManager) LoadPersistedEventsBefore(agentID string, beforeSeq uint64, limit int) ([]eventbuf.Event, error) {
	records, err := em.store.ListConversationEventsBefore(agentID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	events := make([]eventbuf.Event, 0, len(records))
	for _, r := range records {
		data := map[string]any{
			"role":      r.Role,
			"text":      r.Text,
			"raw":       r.Raw,
			"kind":      r.Kind,
			"timestamp": parseEventRowTimestamp(r.CreatedAt),
		}
		for k, v := range r.Payload {
			data[k] = v
		}
		events = append(events, eventbuf.Event{Seq: r.Seq, Data: data})
	}
	return events, nil
}

// LastPersistedSeq returns the highest persisted sequence number for an agent.
func (em *EventManager) LastPersistedSeq(agentID string) (uint64, error) {
	return em.store.LastConversationSeq(agentID)
}

// LastConversationEventTime returns the timestamp of the most recent conversation event.
func (em *EventManager) LastConversationEventTime(agentID string) (time.Time, error) {
	return em.store.LastConversationEventTime(agentID)
}

// ClearConversationEvents removes all persisted conversation events for an agent.
func (em *EventManager) ClearConversationEvents(agentID string) error {
	return em.store.ClearConversationEvents(agentID)
}
