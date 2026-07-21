package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func TestRawResponseRepoInsert(t *testing.T) {
	ctx := context.Background()
	repo := NewRawResponseRepo(testPool)

	reqBody := `{"q":"odds"}`
	capturedAt := time.Date(2030, 7, 1, 12, 0, 0, 0, time.UTC)
	err := repo.Insert(ctx, model.RawAPIResponse{
		Service:      "lines-service",
		Source:       "the_odds_api",
		Endpoint:     "/v4/sports/rawtest/odds",
		HTTPStatus:   200,
		RequestBody:  &reqBody,
		ResponseBody: `[{"id":"ev1"}]`,
		CapturedAt:   capturedAt,
	})
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	var (
		service, source, respBody string
		gotReqBody                *string
		status                    int
		gotCaptured               time.Time
	)
	err = testPool.QueryRow(ctx,
		`SELECT service, source, http_status, request_body, response_body, captured_at
		FROM public.raw_api_responses WHERE endpoint = $1`, "/v4/sports/rawtest/odds",
	).Scan(&service, &source, &status, &gotReqBody, &respBody, &gotCaptured)
	if err != nil {
		t.Fatalf("read back raw response: %v", err)
	}
	if service != "lines-service" || source != "the_odds_api" || status != 200 {
		t.Errorf("row = %s/%s/%d, want lines-service/the_odds_api/200", service, source, status)
	}
	if gotReqBody == nil || *gotReqBody != reqBody {
		t.Errorf("request_body = %v, want %q", gotReqBody, reqBody)
	}
	if respBody != `[{"id":"ev1"}]` || !gotCaptured.Equal(capturedAt) {
		t.Errorf("row = %q at %v, want the archived payload", respBody, gotCaptured)
	}
}
