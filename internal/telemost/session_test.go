package telemost

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// TestCloseSignal verifies the closeSignal helper is idempotent and safe.
func TestCloseSignal(t *testing.T) {
	t.Run("closes open channel", func(t *testing.T) {
		ch := make(chan struct{})
		closeSignal(ch)
		select {
		case <-ch:
			// OK — channel closed
		default:
			t.Error("channel not closed")
		}
	})

	t.Run("idempotent on closed channel", func(t *testing.T) {
		ch := make(chan struct{})
		close(ch)
		// Should not panic
		closeSignal(ch)
	})

	t.Run("nil channel safe", func(t *testing.T) {
		// Should not panic
		closeSignal(nil)
	})
}

// TestQueueReconnect verifies reconnect queueing behavior.
func TestQueueReconnect(t *testing.T) {
	p := &Peer{
		reconnectCh: make(chan struct{}, 1),
	}

	t.Run("queues when not closed/reconnecting", func(t *testing.T) {
		p.queueReconnect()
		select {
		case <-p.reconnectCh:
			// OK
		default:
			t.Error("expected reconnect signal")
		}
	})

	t.Run("skips when closed", func(t *testing.T) {
		p.closed.Store(true)
		p.queueReconnect()
		select {
		case <-p.reconnectCh:
			t.Error("should not queue when closed")
		default:
			// OK
		}
		p.closed.Store(false)
	})

	t.Run("skips when reconnecting", func(t *testing.T) {
		p.reconnecting.Store(true)
		p.queueReconnect()
		select {
		case <-p.reconnectCh:
			t.Error("should not queue when reconnecting")
		default:
			// OK
		}
		p.reconnecting.Store(false)
	})

	t.Run("skips when shutting down", func(t *testing.T) {
		p.shuttingDown.Store(true)
		p.queueReconnect()
		select {
		case <-p.reconnectCh:
			t.Error("should not queue when shutting down")
		default:
			// OK
		}
		p.shuttingDown.Store(false)
	})

	t.Run("non-blocking when queue full", func(t *testing.T) {
		p.reconnectCh <- struct{}{} // fill the buffer
		p.queueReconnect()          // should not block
		// drain
		<-p.reconnectCh
	})
}

// TestDrainReconnectQueue verifies drain empties the channel.
func TestDrainReconnectQueue(t *testing.T) {
	p := &Peer{
		reconnectCh: make(chan struct{}, 1),
	}
	p.reconnectCh <- struct{}{}
	p.drainReconnectQueue()

	select {
	case <-p.reconnectCh:
		t.Error("queue should be empty after drain")
	default:
		// OK
	}
}

// TestResetSession verifies session channels are recreated.
func TestResetSession(t *testing.T) {
	p := &Peer{}
	keepAlive, sessionClose := p.resetSession()

	if keepAlive == nil {
		t.Error("keepAliveCh should not be nil")
	}
	if sessionClose == nil {
		t.Error("sessionCloseCh should not be nil")
	}

	// Channels should be open
	select {
	case <-keepAlive:
		t.Error("keepAliveCh should be open")
	default:
	}
	select {
	case <-sessionClose:
		t.Error("sessionCloseCh should be open")
	default:
	}
}

// TestStopSession verifies session stop closes channels.
func TestStopSession(t *testing.T) {
	p := &Peer{
		telemetryCh: make(chan struct{}, 1),
	}
	keepAlive, sessionClose := p.resetSession()
	p.stopSession()

	select {
	case <-keepAlive:
		// OK — closed
	default:
		t.Error("keepAliveCh should be closed after stopSession")
	}
	select {
	case <-sessionClose:
		// OK — closed
	default:
		t.Error("sessionCloseCh should be closed after stopSession")
	}
}

// TestNewPeerDefaults verifies constructor sets correct defaults.
func TestNewPeerDefaults(t *testing.T) {
	// We can't call NewPeer (requires network), but we can verify struct defaults
	p := &Peer{
		trafficShape: TrafficShape{
			MaxMessageSize: realDataChannelMessageLimit,
			MinDelay:       defaultSendDelayMin,
			MaxDelay:       defaultSendDelayMax,
		},
	}

	if p.trafficShape.MaxMessageSize != 8192 {
		t.Errorf("MaxMessageSize = %d, want 8192", p.trafficShape.MaxMessageSize)
	}
	if p.trafficShape.MinDelay != 2*time.Millisecond {
		t.Errorf("MinDelay = %v, want 2ms", p.trafficShape.MinDelay)
	}
	if p.trafficShape.MaxDelay != 12*time.Millisecond {
		t.Errorf("MaxDelay = %v, want 12ms", p.trafficShape.MaxDelay)
	}
}

// TestSetTrafficShape verifies traffic shape normalization.
func TestSetTrafficShape(t *testing.T) {
	p := &Peer{}

	t.Run("normalizes zero MaxMessageSize", func(t *testing.T) {
		p.SetTrafficShape(TrafficShape{MaxMessageSize: 0, MinDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond})
		if p.trafficShape.MaxMessageSize != realDataChannelMessageLimit {
			t.Errorf("MaxMessageSize = %d, want %d", p.trafficShape.MaxMessageSize, realDataChannelMessageLimit)
		}
	})

	t.Run("normalizes MaxDelay < MinDelay", func(t *testing.T) {
		p.SetTrafficShape(TrafficShape{MaxMessageSize: 1000, MinDelay: 10 * time.Millisecond, MaxDelay: 5 * time.Millisecond})
		if p.trafficShape.MaxDelay != p.trafficShape.MinDelay {
			t.Errorf("MaxDelay = %v, want %v (same as MinDelay)", p.trafficShape.MaxDelay, p.trafficShape.MinDelay)
		}
	})
}

// TestSetReconnectCallback verifies callback is stored.
func TestSetReconnectCallback(t *testing.T) {
	p := &Peer{}
	called := atomic.Bool{}
	p.SetReconnectCallback(func(dc *webrtc.DataChannel) {
		called.Store(true)
	})
	if p.onReconnect == nil {
		t.Error("onReconnect should not be nil")
	}
	p.onReconnect(nil)
	if !called.Load() {
		t.Error("callback was not called")
	}
}

// TestSetEndedCallback verifies callback is stored.
func TestSetEndedCallback(t *testing.T) {
	p := &Peer{}
	var reason string
	p.SetEndedCallback(func(r string) {
		reason = r
	})
	if p.onEnded == nil {
		t.Error("onEnded should not be nil")
	}
	p.onEnded("test reason")
	if reason != "test reason" {
		t.Errorf("reason = %q, want %q", reason, "test reason")
	}
}
