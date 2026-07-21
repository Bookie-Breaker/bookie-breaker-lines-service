package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func newTestContext() (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return body
}

func TestResponseEnvelopes(t *testing.T) {
	tests := []struct {
		name       string
		call       func(echo.Context) error
		wantStatus int
	}{
		{"success", func(c echo.Context) error { return SuccessResponse(c, map[string]string{"k": "v"}) }, http.StatusOK},
		{"created", func(c echo.Context) error { return CreatedResponse(c, map[string]string{"k": "v"}) }, http.StatusCreated},
		{"accepted", func(c echo.Context) error { return AcceptedResponse(c, map[string]string{"k": "v"}) }, http.StatusAccepted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newTestContext()
			if err := tt.call(c); err != nil {
				t.Fatalf("%s failed: %v", tt.name, err)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := decodeBody(t, rec)
			if body["data"].(map[string]any)["k"] != "v" {
				t.Errorf("data = %v, want payload round-tripped", body["data"])
			}
			meta := body["meta"].(map[string]any)
			if meta["timestamp"] == "" {
				t.Error("meta.timestamp missing")
			}
			// No request ID header on the response: newMeta generates one.
			if _, err := uuid.Parse(meta["request_id"].(string)); err != nil {
				t.Errorf("request_id %v is not a UUID: %v", meta["request_id"], err)
			}
		})
	}
}

func TestPaginatedResponse(t *testing.T) {
	c, rec := newTestContext()
	if err := PaginatedResponse(c, []string{"a"}, 25, true, "cursor123"); err != nil {
		t.Fatalf("PaginatedResponse failed: %v", err)
	}
	body := decodeBody(t, rec)
	pag := body["meta"].(map[string]any)["pagination"].(map[string]any)
	if pag["limit"] != float64(25) || pag["has_more"] != true || pag["next_cursor"] != "cursor123" {
		t.Errorf("pagination = %v, want limit/has_more/cursor", pag)
	}

	// next_cursor is omitted when empty.
	c, rec = newTestContext()
	if err := PaginatedResponse(c, []string{}, 50, false, ""); err != nil {
		t.Fatal(err)
	}
	pag = decodeBody(t, rec)["meta"].(map[string]any)["pagination"].(map[string]any)
	if _, present := pag["next_cursor"]; present {
		t.Errorf("pagination = %v, next_cursor should be omitted when empty", pag)
	}
}

func TestErrorResponse(t *testing.T) {
	c, rec := newTestContext()
	if err := ErrorResponse(c, http.StatusTeapot, "TEAPOT", "short and stout"); err != nil {
		t.Fatalf("ErrorResponse failed: %v", err)
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rec.Code)
	}
	body := decodeBody(t, rec)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "TEAPOT" || errObj["message"] != "short and stout" {
		t.Errorf("error = %v, want code and message", errObj)
	}
}

func TestNewMetaEchoesRequestIDHeader(t *testing.T) {
	c, rec := newTestContext()
	c.Response().Header().Set(echo.HeaderXRequestID, "req-abc")
	if err := SuccessResponse(c, nil); err != nil {
		t.Fatal(err)
	}
	body := decodeBody(t, rec)
	if body["meta"].(map[string]any)["request_id"] != "req-abc" {
		t.Errorf("request_id = %v, want the response header value", body["meta"])
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	e := echo.New()
	next := func(c echo.Context) error { return c.NoContent(http.StatusOK) }

	// Absent header: middleware generates a UUID.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := RequestIDMiddleware()(next)(c); err != nil {
		t.Fatal(err)
	}
	got := rec.Header().Get(echo.HeaderXRequestID)
	if _, err := uuid.Parse(got); err != nil {
		t.Errorf("generated request ID %q is not a UUID: %v", got, err)
	}

	// Incoming header is propagated untouched.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderXRequestID, "client-supplied")
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	if err := RequestIDMiddleware()(next)(c); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get(echo.HeaderXRequestID) != "client-supplied" {
		t.Errorf("request ID = %q, want client-supplied echoed back", rec.Header().Get(echo.HeaderXRequestID))
	}
}
