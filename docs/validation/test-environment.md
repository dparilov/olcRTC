# Transport Validation Test Environment

## Architecture

The test harness (`cmd/transport-test`) runs validation scenarios using a
**local loopback transport** — two mux instances connected via encrypted
channels (ChaCha20-Poly1305) without requiring Telemost infrastructure.

```
 ┌──────────┐   encrypt    ┌──────────┐
 │  Mux A   │ ──────────→  │  Mux B   │
 │ (client1)│   chan AB     │ (client2)│
 │          │ ←──────────  │          │
 │          │   chan BA     │          │
 └──────────┘   decrypt    └──────────┘
```

This validates the full mux/crypto stack: framing, chunking, sequencing,
out-of-order handling, backpressure, reset, and stream lifecycle.

## What Is Tested

- **Mux layer**: frame format, stream lifecycle, ordering, OOO buffering
- **Crypto layer**: encrypt/decrypt round-trip integrity
- **Backpressure**: event-driven buffer management (sync.Cond)
- **Stream signals**: WaitForData, StreamClosedCh (post-Item-7)
- **Reset/reconnect**: mux state invalidation and recovery

## What Is NOT Tested (requires Telemost rooms)

- WebRTC/VP8 tunnel path
- Telemost SFU interaction
- Real network latency/jitter
- Room creation/join lifecycle

## Running

```bash
# Build
go build -o transport-test ./cmd/transport-test/

# Quick functional tests
./transport-test -scenario=A,B,H

# Throughput + latency
./transport-test -scenario=C,D,E

# Full soak (20 min)
./transport-test -scenario=F,G -duration=20m

# All scenarios
./transport-test -scenario=all -duration=20m -output=results.json
```

## Cross-compile for VPS
```bash
GOOS=linux GOARCH=amd64 go build -o transport-test-linux ./cmd/transport-test/
scp transport-test-linux root@VPS:/opt/transport-test
```

## Scenarios

| ID | Name | Duration | What it tests |
|----|------|----------|---------------|
| A | Basic send/recv | <1s | Bidirectional data integrity |
| B | Repeated lifecycle | ~1s | 10x create/transfer/reset/destroy |
| C | Unidirectional throughput | 30s | A→B sustained throughput |
| D | Bidirectional throughput | 30s | A↔B simultaneous throughput |
| E | Latency | 25s | RTT under idle/moderate/heavy load |
| F | Soak sustained | 20min | Degradation analysis, steady traffic |
| G | Mixed workload soak | 20min | Alternating payload sizes + bursts |
| H | Reconnect during traffic | 3s | Reset mid-traffic, verify recovery |
