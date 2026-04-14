package oddsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

// GetOdds fetches current odds for a sport.
func (c *Client) GetOdds(ctx context.Context, sport string, markets []string) (*FetchResult, error) {
	ctx, span := tracer.Start(ctx, "oddsapi.GetOdds")
	defer span.End()
	span.SetAttributes(attribute.String("sport", sport))

	url := fmt.Sprintf("%s/v4/sports/%s/odds?apiKey=%s&regions=us&oddsFormat=decimal", c.baseURL, sport, c.apiKey)
	for _, m := range markets {
		url += "&markets=" + m
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	result := &FetchResult{
		RawBody:    body,
		HTTPStatus: resp.StatusCode,
	}

	if used := resp.Header.Get("x-requests-used"); used != "" {
		result.RequestsUsed, _ = strconv.Atoi(used)
	}
	if left := resp.Header.Get("x-requests-remaining"); left != "" {
		result.RequestsLeft, _ = strconv.Atoi(left)
		if result.RequestsLeft < 50 {
			slog.Warn("Odds API quota running low", "remaining", result.RequestsLeft, "used", result.RequestsUsed)
		}
	}

	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
		attribute.Int("oddsapi.requests_remaining", result.RequestsLeft),
	)

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("odds API returned status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &result.Events); err != nil {
		return result, fmt.Errorf("decode response: %w", err)
	}

	return result, nil
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
