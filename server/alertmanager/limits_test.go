package alertmanager

import (
	"strings"
	"testing"
)

func TestDecodeJSONLimited(t *testing.T) {
	t.Run("small payload decodes", func(t *testing.T) {
		var got struct {
			A int `json:"a"`
		}
		if err := DecodeJSONLimited(strings.NewReader(`{"a":1}`), &got); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.A != 1 {
			t.Fatalf("got %d, want 1", got.A)
		}
	})

	t.Run("oversized payload is rejected, not truncated", func(t *testing.T) {
		// A JSON array larger than MaxResponseBytes on the wire.
		huge := "[" + strings.Repeat("0,", MaxResponseBytes) + "0]"
		var sink []int
		if err := DecodeJSONLimited(strings.NewReader(huge), &sink); err == nil {
			t.Fatalf("expected an error for an oversized body, got nil (silent truncation)")
		}
	})
}

func TestReadAllLimited(t *testing.T) {
	b, err := ReadAllLimited(strings.NewReader("short body"))
	if err != nil || string(b) != "short body" {
		t.Fatalf("short read: err=%v body=%q", err, b)
	}
	if _, err := ReadAllLimited(strings.NewReader(strings.Repeat("x", MaxResponseBytes+10))); err == nil {
		t.Fatalf("expected an error for an oversized body, got nil")
	}
}
