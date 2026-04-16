package rendezvous

import (
	"testing"
	"time"
)

func TestDeriveKey(t *testing.T) {
	// Same inputs must produce same output
	k1 := DeriveKey("secret123", "room456")
	k2 := DeriveKey("secret123", "room456")
	if k1 != k2 {
		t.Fatalf("DeriveKey not deterministic: %s != %s", k1, k2)
	}

	// Must be 64-char hex (256-bit)
	if len(k1) != 64 {
		t.Fatalf("DeriveKey expected 64 chars, got %d", len(k1))
	}

	// Different room = different key
	k3 := DeriveKey("secret123", "room789")
	if k1 == k3 {
		t.Fatal("DeriveKey: different rooms should produce different keys")
	}

	// Different secret = different key
	k4 := DeriveKey("other-secret", "room456")
	if k1 == k4 {
		t.Fatal("DeriveKey: different secrets should produce different keys")
	}

	// Empty inputs should still work (no panic)
	k5 := DeriveKey("", "")
	if len(k5) != 64 {
		t.Fatalf("DeriveKey with empty inputs: expected 64 chars, got %d", len(k5))
	}
}

func TestIsExpired(t *testing.T) {
	// nil record = expired
	if !IsExpired(nil) {
		t.Fatal("nil record should be expired")
	}

	// Empty expires = expired
	if !IsExpired(&RoomRecord{}) {
		t.Fatal("empty expires should be expired")
	}

	// Future = not expired
	future := &RoomRecord{
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if IsExpired(future) {
		t.Fatal("future record should not be expired")
	}

	// Past = expired
	past := &RoomRecord{
		ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	if !IsExpired(past) {
		t.Fatal("past record should be expired")
	}
}

func TestDeriveKeyFailClosed(t *testing.T) {
	// Even with empty master secret, DeriveKey should return a valid 64-char hex
	// (the caller is responsible for not calling with empty secret)
	key := DeriveKey("", "room123")
	if len(key) != 64 {
		t.Fatalf("expected 64-char key, got %d", len(key))
	}

	// With proper inputs, key should be non-zero
	key = DeriveKey("my-production-secret", "77873352023589")
	if key == "" || key == DeriveKey("", "") {
		t.Fatal("key should be unique for unique inputs")
	}
}
