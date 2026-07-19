package sharpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const streamPath = "/v1/stream"

// Client connects to the SharpAPI SSE stream.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new SharpAPI streaming client. apiKey may be empty (the
// stub does not authenticate). httpClient may be nil; the default client has
// no timeout because the stream is long-lived — cancellation is via context.
func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: httpClient,
	}
}

// Stream opens GET {baseURL}/v1/stream and emits decoded frames until the
// stream ends or ctx is canceled. The frame channel is closed on either.
// The error channel (capacity 1) receives at most one terminal error;
// malformed frames are logged and skipped, never fatal.
func (c *Client) Stream(ctx context.Context) (<-chan Frame, <-chan error) {
	frames := make(chan Frame)
	errs := make(chan error, 1)

	go func() {
		defer close(frames)
		defer close(errs)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+streamPath, nil)
		if err != nil {
			errs <- fmt.Errorf("create stream request: %w", err)
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				errs <- fmt.Errorf("connect stream: %w", err)
			}
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			errs <- fmt.Errorf("stream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return
		}

		if err := readEvents(ctx, resp.Body, frames); err != nil {
			errs <- err
		}
	}()

	return frames, errs
}

// readEvents parses the SSE wire format: `data:` lines accumulate (multi-line
// data joins with newlines), a blank line dispatches the pending event, and
// comment lines (`: ping` keep-alives) plus unknown fields are ignored.
func readEvents(ctx context.Context, r io.Reader, frames chan<- Frame) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var data []string
	dispatch := func() {
		if len(data) == 0 {
			return
		}
		payload := strings.Join(data, "\n")
		data = data[:0]

		var f Frame
		if err := json.Unmarshal([]byte(payload), &f); err != nil {
			slog.Warn("skipping malformed sharpapi frame", "error", err)
			return
		}
		select {
		case frames <- f:
		case <-ctx.Done():
		}
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := scanner.Text()
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			// Comment / keep-alive line: tolerated, never dispatched.
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// event:, id:, retry:, or unknown fields — ignored.
		}
	}
	// Stream ended without a trailing blank line: dispatch what we have.
	dispatch()

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}
