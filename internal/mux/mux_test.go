package mux

import (
	"time"
	"encoding/binary"
	"sync"
	"testing"
)

// collectSender returns an onSend function that collects all sent frames.
func collectSender() (func([]byte) error, *[][]byte) {
	var mu sync.Mutex
	var frames [][]byte
	return func(data []byte) error {
		mu.Lock()
		cp := make([]byte, len(data))
		copy(cp, data)
		frames = append(frames, cp)
		mu.Unlock()
		return nil
	}, &frames
}

// --- Frame Format Tests ---

func TestFrameFormat_HeaderSize(t *testing.T) {
	send, frames := collectSender()
	m := New(42, send)
	sid := m.OpenStream()
	m.SendData(sid, []byte("hello"))

	if len(*frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(*frames))
	}
	f := (*frames)[0]
	if len(f) < 12 {
		t.Fatalf("frame too short: %d bytes", len(f))
	}

	clientID := binary.BigEndian.Uint32(f[0:4])
	streamID := binary.BigEndian.Uint16(f[4:6])
	length := binary.BigEndian.Uint16(f[6:8])
	seq := binary.BigEndian.Uint32(f[8:12])

	if clientID != 42 {
		t.Errorf("clientID = %d, want 42", clientID)
	}
	if streamID != sid {
		t.Errorf("streamID = %d, want %d", streamID, sid)
	}
	if length != 5 {
		t.Errorf("length = %d, want 5", length)
	}
	if seq != 0 {
		t.Errorf("seq = %d, want 0", seq)
	}
	if string(f[12:]) != "hello" {
		t.Errorf("payload = %q, want %q", f[12:], "hello")
	}
}

func TestFrameFormat_Chunking(t *testing.T) {
	send, frames := collectSender()
	m := New(1, send)
	sid := m.OpenStream()

	// Send 15000 bytes — should produce 3 chunks (7000+7000+1000)
	data := make([]byte, 15000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	m.SendData(sid, data)

	if len(*frames) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(*frames))
	}

	// Verify sequence numbers are monotonic
	for i, f := range *frames {
		seq := binary.BigEndian.Uint32(f[8:12])
		if seq != uint32(i) {
			t.Errorf("chunk %d: seq = %d, want %d", i, seq, i)
		}
		length := binary.BigEndian.Uint16(f[6:8])
		if i < 2 && length != 7000 {
			t.Errorf("chunk %d: length = %d, want 7000", i, length)
		}
		if i == 2 && length != 1000 {
			t.Errorf("chunk %d: length = %d, want 1000", i, length)
		}
	}
}

// --- Stream Lifecycle Tests ---

func TestOpenStream_StartsAt1(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	sid := m.OpenStream()
	if sid != 1 {
		t.Errorf("first stream ID = %d, want 1", sid)
	}
}

func TestOpenStream_Increments(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	s1 := m.OpenStream()
	s2 := m.OpenStream()
	s3 := m.OpenStream()
	if s1 != 1 || s2 != 2 || s3 != 3 {
		t.Errorf("stream IDs = %d, %d, %d; want 1, 2, 3", s1, s2, s3)
	}
}

func TestCloseStream_SendsCloseFrame(t *testing.T) {
	send, frames := collectSender()
	m := New(99, send)
	sid := m.OpenStream()
	m.CloseStream(sid)

	if len(*frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(*frames))
	}
	f := (*frames)[0]
	if len(f) != 12 {
		t.Fatalf("close frame size = %d, want 12", len(f))
	}
	length := binary.BigEndian.Uint16(f[6:8])
	seq := binary.BigEndian.Uint32(f[8:12])
	if length != 0 {
		t.Errorf("close frame length = %d, want 0", length)
	}
	if seq != 0 {
		t.Errorf("close frame seq = %d, want 0", seq)
	}
}

func TestCloseStream_MarksStreamClosed(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	sid := m.OpenStream()
	m.CloseStream(sid)
	if !m.StreamClosed(sid) {
		t.Error("stream should be closed after CloseStream")
	}
}

func TestStreamClosed_NonexistentStream(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	if !m.StreamClosed(999) {
		t.Error("nonexistent stream should report as closed")
	}
}

// --- Ordering Tests ---

func TestOrdering_InOrder(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Simulate receiving 3 frames in order
	for seq := uint32(0); seq < 3; seq++ {
		frame := makeDataFrame(10, 1, uint16(seq+1)*0+1, []byte{byte(seq)}, seq)
		// Use stream ID 1 for all
		binary.BigEndian.PutUint16(frame[4:6], 1)
		m.HandleFrame(frame)
	}

	data := m.ReadStream(1)
	if len(data) != 3 {
		t.Fatalf("expected 3 bytes, got %d", len(data))
	}
	for i, b := range data {
		if b != byte(i) {
			t.Errorf("byte %d = %d, want %d", i, b, i)
		}
	}
}

func TestOrdering_SequenceMonotonic(t *testing.T) {
	send, frames := collectSender()
	m := New(1, send)
	sid := m.OpenStream()

	m.SendData(sid, []byte("a"))
	m.SendData(sid, []byte("b"))
	m.SendData(sid, []byte("c"))

	if len(*frames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(*frames))
	}

	for i, f := range *frames {
		seq := binary.BigEndian.Uint32(f[8:12])
		if seq != uint32(i) {
			t.Errorf("frame %d: seq = %d, want %d", i, seq, i)
		}
	}
}

// --- Out-of-Order Tests ---

func TestOutOfOrder_BufferedAndFlushed(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Send seq 1 first (out of order), then seq 0
	frame1 := makeDataFrame(10, 1, 5, []byte("B"), 1)
	frame0 := makeDataFrame(10, 1, 5, []byte("A"), 0)

	m.HandleFrame(frame1) // buffered
	data := m.ReadStream(5)
	if data != nil {
		t.Error("should have no data yet (seq 0 not received)")
	}

	m.HandleFrame(frame0) // triggers flush
	data = m.ReadStream(5)
	if string(data) != "AB" {
		t.Errorf("data = %q, want %q", data, "AB")
	}
}

func TestOutOfOrder_MaxBuffer(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Send 101 out-of-order frames (seq 1..101, skipping seq 0)
	for seq := uint32(1); seq <= 101; seq++ {
		frame := makeDataFrame(10, 1, 7, []byte{byte(seq)}, seq)
		m.HandleFrame(frame)
	}

	// Only 100 should be buffered
	m.mu.RLock()
	stream := m.streams[7]
	oooCount := len(stream.outOfOrder)
	m.mu.RUnlock()

	if oooCount > 100 {
		t.Errorf("out-of-order buffer = %d, want <= 100", oooCount)
	}
}

func TestOutOfOrder_DuplicateDropped(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	frame0 := makeDataFrame(10, 1, 8, []byte("A"), 0)
	frame0dup := makeDataFrame(10, 1, 8, []byte("X"), 0) // duplicate seq 0

	m.HandleFrame(frame0)
	m.HandleFrame(frame0dup) // should be dropped (seq < nextSeq)

	data := m.ReadStream(8)
	if string(data) != "A" {
		t.Errorf("data = %q, want %q (duplicate should be dropped)", data, "A")
	}
}

// --- Reset Tests ---

func TestReset_ClearsAllState(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	s1 := m.OpenStream()
	s2 := m.OpenStream()

	frame := makeDataFrame(1, 1, s1, []byte("data"), 0)
	m.HandleFrame(frame)

	m.Reset()

	if !m.StreamClosed(s1) {
		t.Error("stream s1 should be closed after Reset")
	}
	if !m.StreamClosed(s2) {
		t.Error("stream s2 should be closed after Reset")
	}

	streams := m.GetStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 streams after Reset, got %d", len(streams))
	}

	// Next stream should start at 1 again
	s3 := m.OpenStream()
	if s3 != 1 {
		t.Errorf("after Reset, next stream = %d, want 1", s3)
	}
}

func TestResetClient_OnlyAffectsMatchingClient(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Create streams for client 10 and client 20
	frame10 := makeDataFrame(10, 1, 1, []byte("c10"), 0)
	frame20 := makeDataFrame(20, 1, 2, []byte("c20"), 0)
	m.HandleFrame(frame10)
	m.HandleFrame(frame20)

	m.ResetClient(10)

	if !m.StreamClosed(1) {
		t.Error("stream 1 (client 10) should be closed")
	}
	if m.StreamClosed(2) {
		t.Error("stream 2 (client 20) should NOT be closed")
	}
}

func TestSendClientReset_Frame(t *testing.T) {
	send, frames := collectSender()
	m := New(42, send)
	m.SendClientReset()

	if len(*frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(*frames))
	}
	f := (*frames)[0]
	cf, ok := ParseControlFrame(f)
	if !ok {
		t.Fatal("expected control frame")
	}
	if cf.ClientID != 42 {
		t.Errorf("clientID = %d, want 42", cf.ClientID)
	}
	if cf.Type != ControlResetClient {
		t.Errorf("type = %d, want %d", cf.Type, ControlResetClient)
	}
}

// --- Control Frame Tests ---

func TestControlFrame_BuildAndParse(t *testing.T) {
	frame := BuildControlFrame(123, ControlResetClient)
	cf, ok := ParseControlFrame(frame)
	if !ok {
		t.Fatal("ParseControlFrame returned false")
	}
	if cf.ClientID != 123 {
		t.Errorf("clientID = %d, want 123", cf.ClientID)
	}
	if cf.Type != ControlResetClient {
		t.Errorf("type = %d, want %d", cf.Type, ControlResetClient)
	}
}

func TestControlFrame_NotDataFrame(t *testing.T) {
	// A normal data frame should NOT parse as control
	frame := makeDataFrame(1, 1, 1, []byte("data"), 0)
	_, ok := ParseControlFrame(frame)
	if ok {
		t.Error("data frame should not parse as control frame")
	}
}

func TestControlFrame_TooShort(t *testing.T) {
	_, ok := ParseControlFrame([]byte{1, 2, 3})
	if ok {
		t.Error("short frame should not parse as control")
	}
}

// --- Close Frame Receive Tests ---

func TestCloseFrame_ReceiveClosesStream(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Create stream via data
	dataFrame := makeDataFrame(10, 1, 3, []byte("hi"), 0)
	m.HandleFrame(dataFrame)

	// Send close frame
	closeFrame := make([]byte, 12)
	binary.BigEndian.PutUint32(closeFrame[0:4], 10)  // clientID
	binary.BigEndian.PutUint16(closeFrame[4:6], 3)    // streamID
	binary.BigEndian.PutUint16(closeFrame[6:8], 0)    // length=0 (close)
	binary.BigEndian.PutUint32(closeFrame[8:12], 0)   // seq=0

	m.HandleFrame(closeFrame)

	if !m.StreamClosed(3) {
		t.Error("stream should be closed after receiving close frame")
	}
}

func TestCloseFrame_WrongClientID_Ignored(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Create stream for client 10
	dataFrame := makeDataFrame(10, 1, 3, []byte("hi"), 0)
	m.HandleFrame(dataFrame)

	// Close from different client — should be ignored
	closeFrame := make([]byte, 12)
	binary.BigEndian.PutUint32(closeFrame[0:4], 99)   // wrong clientID
	binary.BigEndian.PutUint16(closeFrame[4:6], 3)
	binary.BigEndian.PutUint16(closeFrame[6:8], 0)
	binary.BigEndian.PutUint32(closeFrame[8:12], 0)

	m.HandleFrame(closeFrame)

	if m.StreamClosed(3) {
		t.Error("stream should NOT be closed by wrong clientID")
	}
}

// --- Reconnect Reset Tests ---

func TestReconnect_ClientIDMismatch_ResetsStream(t *testing.T) {
	m := New(1, func([]byte) error { return nil })

	// Client 10 sends data on stream 5
	frame1 := makeDataFrame(10, 1, 5, []byte("old"), 0)
	m.HandleFrame(frame1)

	data := m.ReadStream(5)
	if string(data) != "old" {
		t.Fatalf("initial data = %q, want %q", data, "old")
	}

	// Client 20 sends data on same stream 5 — triggers takeover
	frame2 := makeDataFrame(20, 1, 5, []byte("new"), 0)
	m.HandleFrame(frame2)

	data = m.ReadStream(5)
	if string(data) != "new" {
		t.Errorf("after takeover data = %q, want %q", data, "new")
	}

	// Verify stream is now owned by client 20
	m.mu.RLock()
	stream := m.streams[5]
	cid := stream.ClientID
	m.mu.RUnlock()
	if cid != 20 {
		t.Errorf("stream clientID = %d, want 20", cid)
	}
}

func TestReconnect_FullReset_ClearsSendSeq(t *testing.T) {
	send, _ := collectSender()
	m := New(1, send)
	sid := m.OpenStream()

	m.SendData(sid, []byte("a"))
	m.SendData(sid, []byte("b"))

	m.Reset()

	// After reset, new stream should start seq at 0
	sid2 := m.OpenStream()
	m.SendData(sid2, []byte("c"))

	m.sendSeqMu.Lock()
	seq := m.sendSeq[sid2]
	m.sendSeqMu.Unlock()

	// seq should be 1 (was incremented after send)
	if seq != 1 {
		t.Errorf("seq after reset = %d, want 1", seq)
	}
}

// --- Overflow Tests ---

func TestOverflow_MaxStreams(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	m.maxStreams = 3

	// Create 3 streams via receiving
	for i := uint16(1); i <= 3; i++ {
		frame := makeDataFrame(10, 1, i, []byte("x"), 0)
		m.HandleFrame(frame)
	}

	// 4th stream should be dropped
	frame := makeDataFrame(10, 1, 4, []byte("overflow"), 0)
	m.HandleFrame(frame)

	if !m.StreamClosed(4) {
		// Stream 4 should not exist
		m.mu.RLock()
		_, exists := m.streams[4]
		m.mu.RUnlock()
		if exists {
			t.Error("stream 4 should not exist (max streams reached)")
		}
	}
}

func TestOverflow_ShortFrame_Ignored(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	// Frame shorter than 12 bytes should be ignored
	m.HandleFrame([]byte{1, 2, 3, 4, 5})
	streams := m.GetStreams()
	if len(streams) != 0 {
		t.Errorf("expected 0 streams after short frame, got %d", len(streams))
	}
}

func TestOverflow_TruncatedPayload_Ignored(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	// Frame claims length=100 but only has 5 bytes of payload
	frame := make([]byte, 17)
	binary.BigEndian.PutUint32(frame[0:4], 1)
	binary.BigEndian.PutUint16(frame[4:6], 1)
	binary.BigEndian.PutUint16(frame[6:8], 100)
	binary.BigEndian.PutUint32(frame[8:12], 0)
	copy(frame[12:], []byte("short"))

	m.HandleFrame(frame)

	data := m.ReadStream(1)
	if data != nil {
		t.Error("truncated frame should not produce data")
	}
}

// --- WaitForData Tests ---

func TestWaitForData_Signaled(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	ch := m.WaitForData(1)

	// Receive data — should signal
	frame := makeDataFrame(10, 1, 1, []byte("wake"), 0)
	m.HandleFrame(frame)

	select {
	case <-ch:
		// OK
	default:
		t.Error("WaitForData channel should be signaled")
	}
}

// --- Helper ---

func makeDataFrame(clientID uint32, _ uint32, sid uint16, data []byte, seq uint32) []byte {
	frame := make([]byte, 12+len(data))
	binary.BigEndian.PutUint32(frame[0:4], clientID)
	binary.BigEndian.PutUint16(frame[4:6], sid)
	binary.BigEndian.PutUint16(frame[6:8], uint16(len(data)))
	binary.BigEndian.PutUint32(frame[8:12], seq)
	copy(frame[12:], data)
	return frame
}

// --- Backpressure / Cond-driven tests ---

func TestBackpressure_CondWakeOnRead(t *testing.T) {
	m := New(1, func([]byte) error { return nil })
	m.maxBufferSize = 10 // very small buffer

	// Fill buffer with 10 bytes
	frame := makeDataFrame(10, 1, 1, []byte("0123456789"), 0)
	m.HandleFrame(frame)

	// Verify buffer is full
	m.mu.RLock()
	stream := m.streams[1]
	bufLen := len(stream.recvBuf)
	m.mu.RUnlock()
	if bufLen != 10 {
		t.Fatalf("bufLen = %d, want 10", bufLen)
	}

	// HandleFrame with more data should block until ReadStream drains
	done := make(chan struct{})
	go func() {
		frame2 := makeDataFrame(10, 1, 1, []byte("extra"), 1)
		m.HandleFrame(frame2) // will block in waitForBufferSpace
		close(done)
	}()

	// Give HandleFrame goroutine time to enter wait
	select {
	case <-done:
		t.Fatal("HandleFrame should be blocked waiting for buffer space")
	case <-time.After(50 * time.Millisecond):
		// OK - it's blocked
	}

	// Drain buffer — should wake the waiter
	data := m.ReadStream(1)
	if len(data) != 10 {
		t.Fatalf("read %d bytes, want 10", len(data))
	}

	// Now HandleFrame should complete
	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("HandleFrame still blocked after ReadStream drained buffer")
	}

	// Verify the extra data was received
	data2 := m.ReadStream(1)
	if string(data2) != "extra" {
		t.Errorf("data2 = %q, want %q", data2, "extra")
	}
}
