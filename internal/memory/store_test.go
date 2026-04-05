package memory

import (
	"context"
	"testing"
	"time"

	"github.com/17twenty/rally/internal/domain"
)

// mockStore implements a minimal in-memory store for testing logic without a DB.
type mockStore struct {
	events []domain.MemoryEvent
}

func (m *mockStore) save(event domain.MemoryEvent) {
	m.events = append(m.events, event)
}

func (m *mockStore) getRecent(employeeID string, limit int) []domain.MemoryEvent {
	var out []domain.MemoryEvent
	// iterate in reverse (newest appended last)
	for i := len(m.events) - 1; i >= 0 && len(out) < limit; i-- {
		if m.events[i].EmployeeID == employeeID {
			out = append(out, m.events[i])
		}
	}
	return out
}

func (m *mockStore) getByType(employeeID, memType string, limit int) []domain.MemoryEvent {
	var out []domain.MemoryEvent
	for i := len(m.events) - 1; i >= 0 && len(out) < limit; i-- {
		e := m.events[i]
		if e.EmployeeID == employeeID && e.Type == memType {
			out = append(out, e)
		}
	}
	return out
}

// --- Tests for newID ---

func TestNewID(t *testing.T) {
	id1 := newID()
	id2 := newID()
	if id1 == id2 {
		t.Error("newID() returned duplicate IDs")
	}
	if len(id1) != 36 {
		t.Errorf("expected UUID length 36, got %d", len(id1))
	}
}

// --- Tests for BuildMemoryContext ---

func TestBuildMemoryContext(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		input    []domain.MemoryEvent
		wantLen  int // minimum expected length
		contains []string
		empty    bool
	}{
		{
			name:  "empty input",
			input: nil,
			empty: true,
		},
		{
			name: "single episodic",
			input: []domain.MemoryEvent{
				{ID: "1", EmployeeID: "emp1", Type: "episodic", Content: "Met with CEO", CreatedAt: base},
			},
			contains: []string{"Episodic", "Met with CEO"},
		},
		{
			name: "groups all three types",
			input: []domain.MemoryEvent{
				{ID: "1", EmployeeID: "emp1", Type: "episodic", Content: "Episodic event", CreatedAt: base},
				{ID: "2", EmployeeID: "emp1", Type: "reflection", Content: "I learned X", CreatedAt: base},
				{ID: "3", EmployeeID: "emp1", Type: "heuristic", Content: "Always do Y", CreatedAt: base},
			},
			contains: []string{"Heuristic", "Reflection", "Episodic", "Always do Y", "I learned X", "Episodic event"},
		},
		{
			name: "unknown type skipped",
			input: []domain.MemoryEvent{
				{ID: "1", EmployeeID: "emp1", Type: "unknown", Content: "Should be ignored", CreatedAt: base},
				{ID: "2", EmployeeID: "emp1", Type: "heuristic", Content: "Keep it simple", CreatedAt: base},
			},
			contains:    []string{"Keep it simple"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildMemoryContext(tc.input)
			if tc.empty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			for _, s := range tc.contains {
				if !contains(got, s) {
					t.Errorf("expected output to contain %q\nGot:\n%s", s, got)
				}
			}
		})
	}
}

func TestBuildMemoryContextTruncation(t *testing.T) {
	// Generate events that exceed 2000 chars
	var events []domain.MemoryEvent
	for i := 0; i < 50; i++ {
		events = append(events, domain.MemoryEvent{
			ID:         newID(),
			EmployeeID: "emp1",
			Type:       "episodic",
			Content:    "This is a fairly long episodic memory event content that takes up space in the prompt context window.",
			CreatedAt:  time.Now(),
		})
	}
	got := BuildMemoryContext(events)
	if len(got) > maxContextChars+200 { // allow small overshoot for headers
		t.Errorf("output too long: %d chars (max ~%d)", len(got), maxContextChars)
	}
}

// --- Tests for mock store logic (unit, no DB) ---

func TestMockStore_Save(t *testing.T) {
	ctx := context.Background()
	_ = ctx // mock doesn't use context
	ms := &mockStore{}

	tests := []struct {
		name  string
		event domain.MemoryEvent
	}{
		{
			name: "save episodic",
			event: domain.MemoryEvent{
				ID: newID(), EmployeeID: "emp1", Type: "episodic",
				Content: "Did a thing", Metadata: nil, CreatedAt: time.Now(),
			},
		},
		{
			name: "save reflection",
			event: domain.MemoryEvent{
				ID: newID(), EmployeeID: "emp1", Type: "reflection",
				Content: "Learned X", CreatedAt: time.Now(),
			},
		},
		{
			name: "save heuristic",
			event: domain.MemoryEvent{
				ID: newID(), EmployeeID: "emp1", Type: "heuristic",
				Content: "Always verify", CreatedAt: time.Now(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ms.save(tc.event)
		})
	}
	if len(ms.events) != len(tests) {
		t.Errorf("expected %d events, got %d", len(tests), len(ms.events))
	}
}

func TestMockStore_GetRecent(t *testing.T) {
	ms := &mockStore{}
	now := time.Now()

	ms.save(domain.MemoryEvent{ID: "1", EmployeeID: "emp1", Type: "episodic", Content: "first", CreatedAt: now.Add(-2 * time.Minute)})
	ms.save(domain.MemoryEvent{ID: "2", EmployeeID: "emp1", Type: "episodic", Content: "second", CreatedAt: now.Add(-time.Minute)})
	ms.save(domain.MemoryEvent{ID: "3", EmployeeID: "emp1", Type: "episodic", Content: "third", CreatedAt: now})
	ms.save(domain.MemoryEvent{ID: "4", EmployeeID: "emp2", Type: "episodic", Content: "other", CreatedAt: now})

	tests := []struct {
		name       string
		employeeID string
		limit      int
		wantCount  int
		wantFirst  string
	}{
		{"limit 2 emp1", "emp1", 2, 2, "third"},
		{"limit 10 emp1", "emp1", 10, 3, "third"},
		{"emp2", "emp2", 10, 1, "other"},
		{"no results", "emp99", 10, 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ms.getRecent(tc.employeeID, tc.limit)
			if len(got) != tc.wantCount {
				t.Errorf("want %d events, got %d", tc.wantCount, len(got))
			}
			if tc.wantFirst != "" && len(got) > 0 && got[0].Content != tc.wantFirst {
				t.Errorf("want first=%q, got %q", tc.wantFirst, got[0].Content)
			}
		})
	}
}

func TestMockStore_GetByType(t *testing.T) {
	ms := &mockStore{}
	now := time.Now()

	ms.save(domain.MemoryEvent{ID: "1", EmployeeID: "emp1", Type: "episodic", Content: "ep1", CreatedAt: now})
	ms.save(domain.MemoryEvent{ID: "2", EmployeeID: "emp1", Type: "reflection", Content: "ref1", CreatedAt: now})
	ms.save(domain.MemoryEvent{ID: "3", EmployeeID: "emp1", Type: "heuristic", Content: "heu1", CreatedAt: now})
	ms.save(domain.MemoryEvent{ID: "4", EmployeeID: "emp1", Type: "episodic", Content: "ep2", CreatedAt: now})

	tests := []struct {
		memType   string
		wantCount int
	}{
		{"episodic", 2},
		{"reflection", 1},
		{"heuristic", 1},
		{"unknown", 0},
	}

	for _, tc := range tests {
		t.Run(tc.memType, func(t *testing.T) {
			got := ms.getByType("emp1", tc.memType, 10)
			if len(got) != tc.wantCount {
				t.Errorf("type=%q: want %d, got %d", tc.memType, tc.wantCount, len(got))
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
