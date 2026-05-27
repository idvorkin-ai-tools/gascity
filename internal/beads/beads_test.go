package beads

import (
	"testing"
	"time"
)

var (
	_ Tx = (*BdStore)(nil)
	_ Tx = (*CachingStore)(nil)
	_ Tx = (*FileStore)(nil)
	_ Tx = (*MemStore)(nil)
)

func TestIsContainerType(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"convoy", true},
		{"epic", false},
		{"task", false},
		{"message", false},
		{"", false},
		{"CONVOY", false}, // case-sensitive
	}
	for _, tt := range tests {
		if got := IsContainerType(tt.typ); got != tt.want {
			t.Errorf("IsContainerType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestIsMoleculeType(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"molecule", true},
		{"wisp", true},
		{"task", false},
		{"convoy", false},
		{"step", false},
		{"", false},
		{"MOLECULE", false}, // case-sensitive
	}
	for _, tt := range tests {
		if got := IsMoleculeType(tt.typ); got != tt.want {
			t.Errorf("IsMoleculeType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestIsReadyExcludedType(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"merge-request", true},
		{"gate", true},
		{"molecule", true},
		{"step", true},
		{"message", true},
		{"session", true},
		{"agent", true},
		{"role", true},
		{"rig", true},
		{"task", false},
		{"convoy", false},
		{"wisp", false},
		{"", false},
		{"MOLECULE", false}, // case-sensitive
	}
	for _, tt := range tests {
		if got := IsReadyExcludedType(tt.typ); got != tt.want {
			t.Errorf("IsReadyExcludedType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

func TestListQueryCreatedBeforeFiltersBeforeLimit(t *testing.T) {
	base := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	items := []Bead{
		{ID: "newer-2", Title: "newer 2", Status: "closed", CreatedAt: base.Add(2 * time.Minute), Labels: []string{"order-run:digest"}},
		{ID: "newer-1", Title: "newer 1", Status: "closed", CreatedAt: base.Add(time.Minute), Labels: []string{"order-run:digest"}},
		{ID: "older-2", Title: "older 2", Status: "closed", CreatedAt: base.Add(-2 * time.Minute), Labels: []string{"order-run:digest"}},
		{ID: "older-1", Title: "older 1", Status: "closed", CreatedAt: base.Add(-time.Minute), Labels: []string{"order-run:digest"}},
	}

	got := ApplyListQuery(items, ListQuery{
		Label:         "order-run:digest",
		CreatedBefore: base,
		Limit:         1,
		IncludeClosed: true,
		Sort:          SortCreatedDesc,
	})

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %+v", len(got), got)
	}
	if got[0].ID != "older-1" {
		t.Fatalf("got[0].ID = %q, want older-1", got[0].ID)
	}
}

func TestListQueryUpdatedBeforeUsesUpdatedAtWithCreatedFallback(t *testing.T) {
	base := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	items := []Bead{
		{
			ID:        "created-old-updated-new",
			Title:     "created old updated new",
			Status:    "closed",
			CreatedAt: base.Add(-2 * time.Hour),
			UpdatedAt: base.Add(2 * time.Hour),
			Labels:    []string{"stale"},
		},
		{
			ID:        "updated-old",
			Title:     "updated old",
			Status:    "closed",
			CreatedAt: base.Add(-3 * time.Hour),
			UpdatedAt: base.Add(-time.Hour),
			Labels:    []string{"stale"},
		},
		{
			ID:        "legacy-created-old",
			Title:     "legacy created old",
			Status:    "closed",
			CreatedAt: base.Add(-30 * time.Minute),
			Labels:    []string{"stale"},
		},
		{
			ID:        "legacy-created-new",
			Title:     "legacy created new",
			Status:    "closed",
			CreatedAt: base.Add(30 * time.Minute),
			Labels:    []string{"stale"},
		},
	}

	got := ApplyListQuery(items, ListQuery{
		Label:         "stale",
		UpdatedBefore: base,
		IncludeClosed: true,
		Sort:          SortCreatedAsc,
	})

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "updated-old" || got[1].ID != "legacy-created-old" {
		t.Fatalf("got IDs = [%s %s], want [updated-old legacy-created-old]", got[0].ID, got[1].ID)
	}
}
