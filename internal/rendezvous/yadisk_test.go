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

func TestSignVerifyCycle(t *testing.T) {
	record := &RoomRecord{
		RoomID:    "12345678901234",
		RoomURL:   "https://telemost.yandex.ru/j/12345678901234",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(3 * time.Hour).Format(time.RFC3339),
	}
	if err := SignRecord(record, "test-secret", 1); err != nil {
		t.Fatalf("SignRecord failed: %v", err)
	}
	if record.Sig == "" {
		t.Fatal("SignRecord should set Sig")
	}
	if record.RecordID == "" {
		t.Fatal("SignRecord should set RecordID")
	}
	if record.Version != 2 {
		t.Fatalf("expected version 2, got %d", record.Version)
	}
	if record.KeyVersion != 1 {
		t.Fatalf("expected key_version 1, got %d", record.KeyVersion)
	}

	// Verify with correct secret
	if err := VerifyRecord(record, "test-secret"); err != nil {
		t.Fatalf("VerifyRecord failed: %v", err)
	}

	// Verify with wrong secret should fail
	if err := VerifyRecord(record, "wrong-secret"); err == nil {
		t.Fatal("VerifyRecord should fail with wrong secret")
	}
}

func TestUnsignedRecordRejected(t *testing.T) {
	// No sig field
	record := &RoomRecord{Version: 2, RoomID: "123"}
	if err := VerifyRecord(record, "secret"); err == nil {
		t.Fatal("unsigned record should be rejected")
	}

	// Legacy version
	record = &RoomRecord{Version: 1, Sig: "abc"}
	if err := VerifyRecord(record, "secret"); err == nil {
		t.Fatal("legacy version record should be rejected")
	}
}

func TestVerifyRecordMultiSecrets(t *testing.T) {
	record := &RoomRecord{
		RoomID:    "99999999999",
		RoomURL:   "https://telemost.yandex.ru/j/99999999999",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}

	// Sign with "old-secret"
	if err := SignRecord(record, "old-secret", 1); err != nil {
		t.Fatalf("SignRecord failed: %v", err)
	}

	// Verify with current=new, previous=old — should match previous (2)
	matched, err := VerifyRecordMulti(record, "new-secret", "old-secret")
	if err != nil {
		t.Fatalf("VerifyRecordMulti failed: %v", err)
	}
	if matched != 2 {
		t.Fatalf("expected match=2 (previous), got %d", matched)
	}

	// Verify with current=old, no previous — should match current (1)
	matched, err = VerifyRecordMulti(record, "old-secret", "")
	if err != nil {
		t.Fatalf("VerifyRecordMulti failed: %v", err)
	}
	if matched != 1 {
		t.Fatalf("expected match=1 (current), got %d", matched)
	}

	// Verify with both wrong — should fail
	_, err = VerifyRecordMulti(record, "wrong1", "wrong2")
	if err == nil {
		t.Fatal("VerifyRecordMulti should fail with both wrong secrets")
	}
}

func TestRecordIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateRecordID()
		if len(id) != 32 { // 16 bytes = 32 hex chars
			t.Fatalf("expected 32-char record ID, got %d: %s", len(id), id)
		}
		if ids[id] {
			t.Fatalf("duplicate record ID: %s", id)
		}
		ids[id] = true
	}
}

func TestSignatureCannotBeForged(t *testing.T) {
	record := &RoomRecord{
		RoomID:    "55555555555",
		RoomURL:   "https://telemost.yandex.ru/j/55555555555",
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(1 * time.Hour).Format(time.RFC3339),
	}
	if err := SignRecord(record, "real-secret", 1); err != nil {
		t.Fatalf("SignRecord failed: %v", err)
	}

	// Tamper with room ID
	record.RoomID = "66666666666"
	if err := VerifyRecord(record, "real-secret"); err == nil {
		t.Fatal("tampered record should fail verification")
	}
}

func TestRoomIDContract(t *testing.T) {
	// Valid numeric IDs
	valid := []string{"12345", "77873352023589", "0", "999999999999999"}
	for _, id := range valid {
		if !numericRoomIDRe.MatchString(id) {
			t.Errorf("should accept numeric room ID: %s", id)
		}
	}

	// Invalid non-numeric
	invalid := []string{"abc", "123abc", "room-id", "", "12 34", "12.34"}
	for _, id := range invalid {
		if numericRoomIDRe.MatchString(id) {
			t.Errorf("should reject non-numeric room ID: %s", id)
		}
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
