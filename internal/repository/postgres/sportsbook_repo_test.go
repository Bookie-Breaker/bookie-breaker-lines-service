package postgres

import (
	"context"
	"testing"
)

func TestSportsbookRepoGetAllFilters(t *testing.T) {
	ctx := context.Background()
	repo := NewSportsbookRepo(testPool)

	sharpID := seedSportsbook(t, "sbtest-pinnacle", "SBTest Pinnacle", true, true)
	softID := seedSportsbook(t, "sbtest-draftkings", "SBTest DraftKings", false, true)
	inactiveID := seedSportsbook(t, "sbtest-defunct", "SBTest Defunct", false, false)

	boolPtr := func(b bool) *bool { return &b }

	keysOf := func(ids ...string) map[string]bool {
		set := make(map[string]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		return set
	}

	tests := []struct {
		name     string
		isSharp  *bool
		isActive *bool
		want     map[string]bool // seeded IDs that must be present
		exclude  map[string]bool // seeded IDs that must be absent
	}{
		{"no filters returns everything", nil, nil, keysOf(sharpID, softID, inactiveID), nil},
		{"sharp only", boolPtr(true), nil, keysOf(sharpID), keysOf(softID, inactiveID)},
		{"active only", nil, boolPtr(true), keysOf(sharpID, softID), keysOf(inactiveID)},
		{"soft and inactive", boolPtr(false), boolPtr(false), keysOf(inactiveID), keysOf(sharpID, softID)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			books, err := repo.GetAll(ctx, tt.isSharp, tt.isActive)
			if err != nil {
				t.Fatalf("GetAll failed: %v", err)
			}
			got := make(map[string]bool, len(books))
			for _, b := range books {
				got[b.ID] = true
			}
			for id := range tt.want {
				if !got[id] {
					t.Errorf("expected sportsbook %s in result", id)
				}
			}
			for id := range tt.exclude {
				if got[id] {
					t.Errorf("sportsbook %s should be filtered out", id)
				}
			}
		})
	}
}

func TestSportsbookRepoGetAllOrdersByName(t *testing.T) {
	ctx := context.Background()
	repo := NewSportsbookRepo(testPool)

	books, err := repo.GetAll(ctx, nil, nil)
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}
	for i := 1; i < len(books); i++ {
		if books[i-1].Name > books[i].Name {
			t.Fatalf("result not ordered by name: %q before %q", books[i-1].Name, books[i].Name)
		}
	}
}

func TestSportsbookRepoGetByKey(t *testing.T) {
	ctx := context.Background()
	repo := NewSportsbookRepo(testPool)
	id := seedSportsbook(t, "sbtest-bykey", "SBTest ByKey", true, true)

	book, err := repo.GetByKey(ctx, "sbtest-bykey")
	if err != nil {
		t.Fatalf("GetByKey failed: %v", err)
	}
	if book.ID != id || book.Name != "SBTest ByKey" || !book.IsSharp || !book.IsActive {
		t.Errorf("book = %+v, want the seeded row", book)
	}
	if book.CreatedAt.IsZero() || book.UpdatedAt.IsZero() {
		t.Error("timestamps should be populated")
	}

	if _, err := repo.GetByKey(ctx, "sbtest-missing"); err == nil {
		t.Error("GetByKey should fail for an unknown key")
	}
}

func TestSportsbookRepoGetByID(t *testing.T) {
	ctx := context.Background()
	repo := NewSportsbookRepo(testPool)
	id := seedSportsbook(t, "sbtest-byid", "SBTest ByID", false, true)

	book, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if book.Key != "sbtest-byid" {
		t.Errorf("key = %q, want sbtest-byid", book.Key)
	}

	if _, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Error("GetByID should fail for an unknown id")
	}
}
