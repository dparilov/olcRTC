package telemost

import (
	"testing"
)

func TestParseMids(t *testing.T) {
	tests := []struct {
		name      string
		sdp       string
		wantAudio string
		wantVideo string
	}{
		{
			name:      "typical SDP with audio and video",
			sdp:       "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:1\r\n",
			wantAudio: "0",
			wantVideo: "1",
		},
		{
			name:      "video only",
			sdp:       "v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:2\r\n",
			wantAudio: "",
			wantVideo: "2",
		},
		{
			name:      "audio only",
			sdp:       "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:audio0\r\n",
			wantAudio: "audio0",
			wantVideo: "",
		},
		{
			name:      "empty SDP",
			sdp:       "",
			wantAudio: "",
			wantVideo: "",
		},
		{
			name:      "multiple audio/video - takes first",
			sdp:       "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:a1\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:a2\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:v1\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:v2\r\n",
			wantAudio: "a1",
			wantVideo: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			audio, video := parseMids(tt.sdp)
			if audio != tt.wantAudio {
				t.Errorf("parseMids() audioMid = %q, want %q", audio, tt.wantAudio)
			}
			if video != tt.wantVideo {
				t.Errorf("parseMids() videoMid = %q, want %q", video, tt.wantVideo)
			}
		})
	}
}

func TestExtractUfrag(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		want      string
	}{
		{
			name:      "typical candidate with ufrag",
			candidate: "candidate:1 1 udp 2130706431 192.168.1.1 12345 typ host ufrag abc123",
			want:      "abc123",
		},
		{
			name:      "no ufrag",
			candidate: "candidate:1 1 udp 2130706431 192.168.1.1 12345 typ host",
			want:      "",
		},
		{
			name:      "empty",
			candidate: "",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUfrag(tt.candidate)
			if got != tt.want {
				t.Errorf("extractUfrag() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsConferenceEndMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  map[string]interface{}
		want bool
	}{
		{
			name: "conferenceClosed",
			msg:  map[string]interface{}{"conferenceClosed": map[string]interface{}{}},
			want: true,
		},
		{
			name: "conferenceEnded",
			msg:  map[string]interface{}{"conferenceEnded": map[string]interface{}{}},
			want: true,
		},
		{
			name: "roomClosed",
			msg:  map[string]interface{}{"roomClosed": map[string]interface{}{}},
			want: true,
		},
		{
			name: "conferenceState closed",
			msg:  map[string]interface{}{"conferenceState": map[string]interface{}{"state": "closed"}},
			want: true,
		},
		{
			name: "conferenceState ended",
			msg:  map[string]interface{}{"conferenceState": map[string]interface{}{"state": "Ended"}},
			want: true,
		},
		{
			name: "conference state terminated",
			msg:  map[string]interface{}{"conference": map[string]interface{}{"state": "TERMINATED"}},
			want: true,
		},
		{
			name: "normal message - not ended",
			msg:  map[string]interface{}{"subscriberSdpOffer": map[string]interface{}{}},
			want: false,
		},
		{
			name: "ack - not ended",
			msg:  map[string]interface{}{"ack": map[string]interface{}{}},
			want: false,
		},
		{
			name: "empty message",
			msg:  map[string]interface{}{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConferenceEndMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isConferenceEndMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsEndedState(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"closed", true},
		{"ended", true},
		{"finished", true},
		{"terminated", true},
		{"Closed", true},
		{"ENDED", true},
		{"active", false},
		{"", false},
		{"starting", false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			got := isEndedState(tt.state)
			if got != tt.want {
				t.Errorf("isEndedState(%q) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}
