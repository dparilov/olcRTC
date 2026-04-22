package telemost

import (
	"bytes"
	"testing"
)

func TestBuildAndExtractDataFrame(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"small payload", []byte("hello")},
		{"empty payload", []byte{}},
		{"binary payload", []byte{0x00, 0xFF, 0x01, 0xFE}},
		{"large payload", bytes.Repeat([]byte("A"), 8000)},
		{"single byte", []byte{42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := buildDataFrame(tt.payload)

			// Frame must start with vp8DataPrefix
			if !bytes.HasPrefix(frame, vp8DataPrefix) {
				t.Error("frame does not start with vp8DataPrefix")
			}

			// Extract must return original payload
			extracted := ExtractDataFromPayload(frame)
			if len(tt.payload) == 0 {
				// Empty payload → dataLen=0 → returns nil
				if extracted != nil {
					t.Errorf("expected nil for empty payload, got %v", extracted)
				}
			} else {
				if !bytes.Equal(extracted, tt.payload) {
					t.Errorf("round-trip failed: got %d bytes, want %d bytes", len(extracted), len(tt.payload))
				}
			}
		})
	}
}

func TestExtractDataFromPayload_Keepalive(t *testing.T) {
	// Keepalive frames should return nil (not data frames)
	tests := []struct {
		name  string
		frame []byte
	}{
		{"vp8 keyframe", vp8Keyframe},
		{"vp8 interframe", vp8Interframe},
		{"too short", []byte{1, 2, 3}},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractDataFromPayload(tt.frame)
			if result != nil {
				t.Errorf("expected nil for keepalive/non-data frame, got %d bytes", len(result))
			}
		})
	}
}

func TestDataFrameMarker(t *testing.T) {
	// Verify the marker byte is 0xFF
	if DataFrameMarker != 0xFF {
		t.Errorf("DataFrameMarker = 0x%02X, want 0xFF", DataFrameMarker)
	}
}
