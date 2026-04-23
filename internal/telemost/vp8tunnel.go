package telemost

import (
	"encoding/binary"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const DataFrameMarker = 0xFF

// forensicMode enables detailed VP8 frame logging for diagnostics.
// Set OLCRTC_FORENSIC=1 to enable.
var forensicMode = os.Getenv("OLCRTC_FORENSIC") == "1"

// vp8DataPrefix is prepended to data frames so the SFU sees a valid
// VP8 interframe header and forwards the packet instead of dropping it.
// Without this prefix, frames starting with 0xFF are invalid VP8 and
// the Telemost SFU silently drops them.
var vp8DataPrefix = []byte{
	177, 1, 0, 8, 17, 24, 0, 24, 0, 24, 88, 47, 244, 0, 8, 0, 0,
}

// buildDataFrame wraps payload into a VP8-valid frame:
// [vp8DataPrefix (17 bytes)][0xFF][uint32 big-endian length][payload]
// The SFU sees a valid VP8 interframe and forwards it to subscribers.
func buildDataFrame(data []byte) []byte {
	pLen := len(vp8DataPrefix)
	frame := make([]byte, pLen+1+4+len(data))
	copy(frame[:pLen], vp8DataPrefix)
	frame[pLen] = DataFrameMarker
	binary.BigEndian.PutUint32(frame[pLen+1:pLen+5], uint32(len(data)))
	copy(frame[pLen+5:], data)
	return frame
}

// ExtractDataFromPayload extracts embedded data from a VP8 frame.
// Returns nil if the frame is a keepalive (not a data frame).
// Data frames are longer than the 17-byte interframe prefix and
// contain a 0xFF marker immediately after it.
func ExtractDataFromPayload(payload []byte) []byte {
	pLen := len(vp8DataPrefix)
	// Must be longer than prefix + marker + length header
	if len(payload) <= pLen+5 {
		return nil
	}
	if payload[pLen] != DataFrameMarker {
		return nil
	}
	dataLen := binary.BigEndian.Uint32(payload[pLen+1 : pLen+5])
	if dataLen == 0 || int(dataLen) > len(payload)-pLen-5 {
		return nil
	}
	return payload[pLen+5 : pLen+5+int(dataLen)]
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
		sendQueue: make(chan []byte, 4096),
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
// maxBurstFrames limits how many data frames we send in one burst
// before yielding to keepalive. Prevents starving the SFU heartbeat.
const maxBurstFrames = 50

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
			// Send first frame
			s.sendDataFrame(data, interval)

			// Drain loop: send up to maxBurstFrames without waiting
			for i := 0; i < maxBurstFrames; i++ {
				select {
				case more := <-s.sendQueue:
					s.sendDataFrame(more, interval)
				default:
					goto drained
				}
			}
		drained:
			// After burst, reset ticker for next keepalive
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
			if s.frameCount <= 3 || s.frameCount%25 == 0 {
				log.Printf("[VP8TX-FORENSIC] KEEPALIVE frame=%d size=%d first=0x%02x duration=%v err=%v",
					s.frameCount, len(frame), frame[0], interval, err)
			}
		}
	}
}

func (s *VP8Sender) sendDataFrame(data []byte, interval time.Duration) {
	s.frameCount++
	frame := buildDataFrame(data)
	err := s.track.WriteSample(media.Sample{Data: frame, Duration: interval})
	// FORENSIC: log every data frame for Linux vs Android comparison
	log.Printf("[VP8TX-FORENSIC] DATA frame=%d size=%d dataLen=%d first=0x%02x duration=%v err=%v queueLen=%d",
		s.frameCount, len(frame), len(data), frame[0], interval, err, len(s.sendQueue))
	// Periodic keyframe to keep SFU happy
	if s.frameCount%60 == 0 {
		s.frameCount++
		s.track.WriteSample(media.Sample{Data: vp8Keyframe, Duration: interval})
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
			data := ExtractDataFromPayload(frameBuf)
			kind := "KEEPALIVE"
			if data != nil {
				kind = "DATA"
			}
			if frameCount <= 10 || data != nil || frameCount%100 == 0 {
				log.Printf("[VP8RX-FORENSIC] frame=#%d size=%d first=0x%02x kind=%s dataLen=%d",
					frameCount, len(frameBuf), frameBuf[0], kind, len(data))
			}
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
