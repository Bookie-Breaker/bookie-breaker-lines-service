package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func TestGetSportsbooks(t *testing.T) {
	repo := &fakeSBRepo{books: []model.Sportsbook{
		{ID: "sb-1", Name: "DraftKings", Key: "draftkings", IsActive: true},
		{ID: "sb-2", Name: "Pinnacle", Key: "pinnacle", IsSharp: true, IsActive: true},
	}}
	h := NewSportsbooksHandler(repo)

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/sportsbooks?is_sharp=true&is_active=true", h.GetSportsbooks, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data = %v, want 2 books", data)
	}
	if repo.gotIsSharp == nil || !*repo.gotIsSharp {
		t.Error("is_sharp=true should be forwarded to the repository")
	}
	if repo.gotIsActive == nil || !*repo.gotIsActive {
		t.Error("is_active=true should be forwarded to the repository")
	}
	assertMeta(t, body)
}

func TestGetSportsbooksNoFilters(t *testing.T) {
	repo := &fakeSBRepo{}
	h := NewSportsbooksHandler(repo)

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/sportsbooks", h.GetSportsbooks, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if repo.gotIsSharp != nil || repo.gotIsActive != nil {
		t.Error("absent filters must be forwarded as nil")
	}
	// Empty registry serializes as [] rather than null.
	if data, ok := body["data"].([]any); !ok || len(data) != 0 {
		t.Errorf("data = %v, want []", body["data"])
	}
}

func TestGetSportsbooksInvalidFilters(t *testing.T) {
	h := NewSportsbooksHandler(&fakeSBRepo{})

	for _, query := range []string{"is_sharp=banana", "is_active=banana"} {
		rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/sportsbooks?"+query, h.GetSportsbooks, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", query, rec.Code)
		}
		assertErrorCode(t, body, "INVALID_PARAMETER")
	}
}

func TestGetSportsbooksRepoError(t *testing.T) {
	h := NewSportsbooksHandler(&fakeSBRepo{err: errors.New("db down")})

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/sportsbooks", h.GetSportsbooks, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertErrorCode(t, body, "INTERNAL_ERROR")
}

func TestParseOptionalBool(t *testing.T) {
	if v, err := parseOptionalBool(""); v != nil || err != nil {
		t.Errorf("parseOptionalBool(\"\") = (%v, %v), want (nil, nil)", v, err)
	}
	if v, err := parseOptionalBool("true"); err != nil || v == nil || !*v {
		t.Errorf("parseOptionalBool(true) = (%v, %v), want true", v, err)
	}
	if v, err := parseOptionalBool("0"); err != nil || v == nil || *v {
		t.Errorf("parseOptionalBool(0) = (%v, %v), want false", v, err)
	}
	if _, err := parseOptionalBool("banana"); err == nil {
		t.Error("parseOptionalBool(banana) should fail")
	}
}
