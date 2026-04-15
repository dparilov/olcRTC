package telemost

import (
	"encoding/binary"
	"log"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const DataFrameMarker = 0xFF

// buildDataFrame wraps payload into a VP8-looking frame:
// [0xFF][uint32 big-endian length][payload]
func buildDataFrame(data []byte) []byte {
	frame := make([]byte, 1+4+len(data))
	frame[0] = DataFrameMarker
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[5:], data)
	return frame
}

// ExtractDataFromPayload extracts embedded data from a VP8 frame.
// Returns nil if the frame is a keepalive (not a data frame).
func ExtractDataFromPayload(payload []byte) []byte {
	if len(payload) < 5 {
		return nil
	}
	if payload[0] != DataFrameMarker {
		return nil
	}
	dataLen := binary.BigEndian.Uint32(payload[1:5])
	if dataLen == 0 || int(dataLen) > len(payload)-5 {
		return nil
	}
	return payload[5 : 5+dataLen]
}

// VP8Sender sends data embedded in VP8 video frames via a sample track.
type VP8Sender struct {
	track      *webrtc.TrackLocalStaticSample
	sendQueue  chan []byte
	frameCount uint64
	fps        int
}

func NewVP8Sender(track *webrtc.TrackLocalStaticSample, fps int) *VP8Sender {
	return &VP8Sender{
		track:     track,
		sendQueue: make(chan []byte, 256),
		fps:       fps,
	}
}

var vp8SendCount atomic.Uint64

func (s *VP8Sender) SendData(data []byte) {
	n := vp8SendCount.Add(1)
	if n <= 5 || n%200 == 0 {
		log.Printf("[VP8TX] SendData #%d len=%d queueLen=%d", n, len(data), len(s.sendQueue))
	}
	select {
	case s.sendQueue <- data:
	default:
		log.Printf("[VP8TX] Queue full, dropping frame len=%d", len(data))
	}
}

// Run sends data and keepalive frames. Blocks until sessionClose or peerClose.
func (s *VP8Sender) Run(sessionClose, peerClose <-chan struct{}) {
	interval := time.Second / time.Duration(s.fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sessionClose:
			return
		case <-peerClose:
			return
		case data := <-s.sendQueue:
			s.frameCount++
			frame := buildDataFrame(data)
			err := s.track.WriteSample(media.Sample{Data: frame, Duration: interval})
			if err != nil {
				log.Printf("[VP8TX] WriteSample DATA error frame=%d: %v", s.frameCount, err)
			} else if s.frameCount <= 10 || s.frameCount%200 == 0 {
				log.Printf("[VP8TX] DATA frame=%d size=%d dataLen=%d", s.frameCount, len(frame), len(data))
			}
			// Periodic keyframe to keep SFU happy
			if s.frameCount%60 == 0 {
				s.frameCount++
				s.track.WriteSample(media.Sample{Data: vp8Keyframe, Duration: interval})
			}
			ticker.Reset(interval)

		case <-ticker.C:
			// Keepalive
			s.frameCount++
			var frame []byte
			if s.frameCount%60 == 0 {
				frame = vp8Keyframe
			} else {
				frame = vp8Interframe
			}
			err := s.track.WriteSample(media.Sample{Data: frame, Duration: interval})
			if s.frameCount <= 3 || s.frameCount%500 == 0 {
				log.Printf("[VP8TX] keepalive frame=%d first=0x%02x err=%v", s.frameCount, frame[0], err)
			}
		}
	}
}

// ReadVP8Track reads RTP packets from a remote VP8 track, reassembles
// frames, and calls onData for each extracted data payload.
// ReadVP8Track reads RTP packets from a remote VP8 track, manually
// depacketizes VP8 frames (matching reference impl), and calls onData
// for each extracted data payload.
func ReadVP8Track(track *webrtc.TrackRemote, onData func([]byte), closeCh <-chan struct{}) {
	log.Printf("[VP8RX] Starting reader for track %s (codec=%s)", track.ID(), track.Codec().MimeType)

	var vp8Pkt codecs.VP8Packet
	var frameBuf []byte
	var frameCount uint64
	var dataCount uint64
	buf := make([]byte, 65535)

	for {
		select {
		case <-closeCh:
			log.Println("[VP8RX] Reader stopped (close signal)")
			return
		default:
		}

		n, _, err := track.Read(buf)
		if err != nil {
			log.Printf("[VP8RX] Read error: %v", err)
			return
		}

		pkt := &rtp.Packet{}
		if pkt.Unmarshal(buf[:n]) != nil {
			continue
		}

		vp8Payload, err := vp8Pkt.Unmarshal(pkt.Payload)
		if err != nil {
			continue
		}

		// S bit = start of VP8 partition → reset frame buffer
		if vp8Pkt.S == 1 {
			frameBuf = frameBuf[:0]
		}
		frameBuf = append(frameBuf, vp8Payload...)

		// Marker bit = end of frame → process complete frame
		if pkt.Marker {
			frameCount++
			if frameCount <= 3 || frameCount%25 == 0 {
				if len(frameBuf) > 0 {
					log.Printf("[VP8RX] frame #%d %d bytes first=0x%02x", frameCount, len(frameBuf), frameBuf[0])
				}
			}
			data := ExtractDataFromPayload(frameBuf)
			if data != nil {
				dataCount++
				if dataCount <= 5 || dataCount%100 == 0 {
					log.Printf("[VP8RX] TUNNEL DATA #%d: %d bytes", dataCount, len(data))
				}
				if onData != nil {
					onData(data)
				}
			}
		}
	}
}
