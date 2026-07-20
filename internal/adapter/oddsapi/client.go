package oddsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("oddsapi-client")

// Client interacts with The Odds API (https://the-odds-api.com).
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Odds API client.
func NewClient(apiKey, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.the-odds-api.com"
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchResult wraps the parsed response and raw body for archival.
type FetchResult struct {
	Events       OddsResponse
	RawBody      []byte
	HTTPStatus   int
	RequestsUsed int
	RequestsLeft int
}

// EventsResult wraps the parsed events list and raw body for archival.
type EventsResult struct {
	Events       []EventStub
	RawBody      []byte
	HTTPStatus   int
	RequestsUsed int
	RequestsLeft int
}

// EventOddsResult wraps a single event's odds response. Unlike the bulk odds
// endpoint, GET /v4/sports/{sport}/events/{eventId}/odds returns one Event
// object, not an array.
type EventOddsResult struct {
	Event        Event
	RawBody      []byte
	HTTPStatus   int
	RequestsUsed int
	RequestsLeft int
}

// GetOdds fetches current odds for a sport.
func (c *Client) GetOdds(ctx context.Context, sport string, markets []string) (*FetchResult, error) {
	ctx, span := tracer.Start(ctx, "oddsapi.GetOdds")
	defer span.End()
	span.SetAttributes(attribute.String("sport", sport))

	url := fmt.Sprintf("%s/v4/sports/%s/odds?apiKey=%s&regions=us&oddsFormat=decimal", c.baseURL, sport, c.apiKey)
	if len(markets) > 0 {
		url += "&markets=" + strings.Join(markets, ",")
	}

	body, status, used, left, err := c.doGet(ctx, span, url)
	result := &FetchResult{
		RawBody:      body,
		HTTPStatus:   status,
		RequestsUsed: used,
		RequestsLeft: left,
	}
	if err != nil {
		if body == nil {
			return nil, err
		}
		return result, err
	}

	if err := json.Unmarshal(body, &result.Events); err != nil {
		return result, fmt.Errorf("decode response: %w", err)
	}

	return result, nil
}

// GetEvents fetches the upcoming events list for a sport (no odds). Quota
// headers are tracked the same way as odds calls; the prop ingestion path
// uses this list to decide which per-event odds calls are worth spending.
func (c *Client) GetEvents(ctx context.Context, sport string) (*EventsResult, error) {
	ctx, span := tracer.Start(ctx, "oddsapi.GetEvents")
	defer span.End()
	span.SetAttributes(attribute.String("sport", sport))

	url := fmt.Sprintf("%s/v4/sports/%s/events?apiKey=%s", c.baseURL, sport, c.apiKey)

	body, status, used, left, err := c.doGet(ctx, span, url)
	result := &EventsResult{
		RawBody:      body,
		HTTPStatus:   status,
		RequestsUsed: used,
		RequestsLeft: left,
	}
	if err != nil {
		if body == nil {
			return nil, err
		}
		return result, err
	}

	if err := json.Unmarshal(body, &result.Events); err != nil {
		return result, fmt.Errorf("decode response: %w", err)
	}

	return result, nil
}

// GetEventOdds fetches odds for a single event, used for player-prop markets
// which The Odds API only serves per event. The response is a single Event
// object (not an array).
func (c *Client) GetEventOdds(ctx context.Context, sport, eventID string, markets []string) (*EventOddsResult, error) {
	ctx, span := tracer.Start(ctx, "oddsapi.GetEventOdds")
	defer span.End()
	span.SetAttributes(
		attribute.String("sport", sport),
		attribute.String("event_id", eventID),
	)

	url := fmt.Sprintf("%s/v4/sports/%s/events/%s/odds?apiKey=%s&regions=us&oddsFormat=decimal",
		c.baseURL, sport, eventID, c.apiKey)
	if len(markets) > 0 {
		url += "&markets=" + strings.Join(markets, ",")
	}

	body, status, used, left, err := c.doGet(ctx, span, url)
	result := &EventOddsResult{
		RawBody:      body,
		HTTPStatus:   status,
		RequestsUsed: used,
		RequestsLeft: left,
	}
	if err != nil {
		if body == nil {
			return nil, err
		}
		return result, err
	}

	if err := json.Unmarshal(body, &result.Event); err != nil {
		return result, fmt.Errorf("decode response: %w", err)
	}

	return result, nil
}

// doGet executes a GET, reads the body, and extracts the x-requests-used /
// x-requests-remaining quota headers shared by all Odds API endpoints. On a
// non-2xx status it still returns the body and quota values alongside the
// error so callers can archive the failure.
func (c *Client) doGet(ctx context.Context, span trace.Span, url string) (body []byte, status, used, left int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("read response body: %w", err)
	}
	status = resp.StatusCode

	if v := resp.Header.Get("x-requests-used"); v != "" {
		used, _ = strconv.Atoi(v)
	}
	if v := resp.Header.Get("x-requests-remaining"); v != "" {
		left, _ = strconv.Atoi(v)
		if left < 50 {
			slog.Warn("Odds API quota running low", "remaining", left, "used", used)
		}
	}

	span.SetAttributes(
		attribute.Int("http.status_code", status),
		attribute.Int("oddsapi.requests_remaining", left),
	)

	if status != http.StatusOK {
		return body, status, used, left, fmt.Errorf("odds API returned status %d: %s", status, string(body))
	}

	return body, status, used, left, nil
}

// GetSports fetches available sports.
func (c *Client) GetSports(ctx context.Context) ([]SportResponse, error) {
	ctx, span := tracer.Start(ctx, "oddsapi.GetSports")
	defer span.End()

	url := fmt.Sprintf("%s/v4/sports?apiKey=%s", c.baseURL, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("odds API returned status %d: %s", resp.StatusCode, string(body))
	}

	var sports []SportResponse
	if err := json.NewDecoder(resp.Body).Decode(&sports); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return sports, nil
}
