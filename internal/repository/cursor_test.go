package repository

import (
	"encoding/base64"
	"testing"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func TestCursorRoundTrip(t *testing.T) {
	in := LineKey{
		GameExternalID: "abc123",
		SportsbookID:   "550e8400-e29b-41d4-a716-446655440000",
		MarketType:     model.MarketSpread,
		Selection:      "Lakers -3.5",
	}

	encoded := EncodeCursor(in)
	if encoded == "" {
		t.Fatal("EncodeCursor returned empty string")
	}

	out, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor failed: %v", err)
	}
	if out != in {
		t.Errorf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	cases := map[string]string{
		"garbage":          "not-base64!!!",
		"non-json":         base64.RawURLEncoding.EncodeToString([]byte("hello")),
		"missing fields":   base64.RawURLEncoding.EncodeToString([]byte(`{"g":"abc"}`)),
		"empty string":     "",
		"valid json array": base64.RawURLEncoding.EncodeToString([]byte(`[1,2,3]`)),
	}

	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCursor(cursor); err == nil {
				t.Errorf("expected error for %q, got nil", cursor)
			}
		})
	}
}
