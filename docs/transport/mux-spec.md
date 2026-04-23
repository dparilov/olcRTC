# olcRTC Mux Protocol Specification

## 1. Overview

The mux layer multiplexes multiple logical byte streams over a single
unreliable datagram transport (WebRTC DataChannel / VP8 tunnel).
Each stream carries a bidirectional TCP-like byte flow (typically a
SOCKS5-proxied connection).

## 2. Frame Format

All frames are 12 bytes header + variable payload.

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          Client ID                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|          Stream ID            |            Length              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Sequence Number                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        Payload (Length bytes)                  |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### Field Definitions

| Field | Offset | Size | Description |
|-------|--------|------|-------------|
| Client ID | 0 | 4 bytes | Identifies the sending client (uint32, big-endian) |
| Stream ID | 4 | 2 bytes | Logical stream identifier (uint16, big-endian). 0 is unused. |
| Length | 6 | 2 bytes | Payload length in bytes (uint16, big-endian). 0 = close frame. |
| Sequence | 8 | 4 bytes | Per-stream monotonic sequence number (uint32, big-endian) |
| Payload | 12 | Length bytes | Application data |

### Reserved Values

- **Stream ID 0xFFFF** + **Length 0xFFFF**: Control frame (see Section 5)
- **Length 0** (with normal Stream ID): Close frame (see Section 4.3)

## 3. Chunking

Large payloads are split into chunks before framing:

- **Max chunk size**: 7000 bytes
- Each chunk gets its own frame with incrementing sequence number
- Receiver reassembles by consuming chunks in sequence order
- Chunk boundaries are transparent to the stream consumer

**Invariant**: `len(payload) <= 7000` for all data frames.

## 4. Stream Lifecycle

### 4.1 Stream Creation

- **Sender side**: `OpenStream()` allocates the next available Stream ID (1, 2, 3, ...)
  - Stream ID 0 is never used
  - Stream IDs wrap around from 0xFFFE back to 1 (0xFFFF is reserved)
  - If a Stream ID is already in use, it is skipped
- **Receiver side**: Streams are auto-created on first data frame received
  - If max streams (10,000) is reached, the frame is silently dropped

### 4.2 Data Transfer

- Sender calls `SendData(sid, data)` which chunks and frames the data
- Each chunk frame carries a monotonically increasing per-stream sequence number
- Receiver buffers frames and delivers data in sequence order

### 4.3 Stream Close

A close frame is a frame with **Length = 0** and **Sequence = 0**:

```
[ClientID][StreamID][0x0000][0x00000000]
```

On send: stream is marked closed, send sequence state is cleaned up.
On receive: stream is marked closed if ClientID matches.

**Invariant**: Once closed, a stream does not accept further data.

### 4.4 Stream Reset (Client-level)

A client reset closes and removes ALL streams belonging to a specific ClientID.
See Section 5.

### 4.5 Client ID Mismatch (Stream Takeover)

If a data frame arrives for an existing stream but with a **different ClientID**,
the stream is fully reset:
- recvBuf cleared
- sequence reset to 0
- outOfOrder buffer cleared
- closed flag cleared
- new ClientID assigned

This handles reconnect scenarios where a new client reuses stream IDs.

## 5. Control Frames

Control frames use reserved sentinel values:

```
[ClientID][0xFFFF][0xFFFF][ControlType]
```

### Control Types

| Type | Value | Description |
|------|-------|-------------|
| ResetClient | 1 | Remove all streams for the given ClientID |

### Detection

A frame is a control frame iff `StreamID == 0xFFFF AND Length == 0xFFFF`.

## 6. Ordering and Out-of-Order Handling

### 6.1 Ordering Guarantees

- Data is delivered to the stream consumer **in sequence order**
- Each stream maintains an independent sequence counter starting at 0
- Frames arriving in order (seq == nextSeq) are appended immediately

### 6.2 Out-of-Order Buffering

- If `seq > nextSeq`: frame is buffered in an out-of-order map
- **Max out-of-order buffer**: 100 frames per stream
- When the expected sequence arrives, buffered frames are flushed in order
- If `seq < nextSeq`: frame is silently dropped (duplicate/stale)

### 6.3 Sequence Wrap

Sequence numbers are uint32. Wrap-around is not explicitly handled
(would require ~4 billion frames per stream to trigger).

## 7. Overflow / Backpressure

### 7.1 Receive Buffer Limit

- **Max buffer size per stream**: 32 MiB
- When a stream's recvBuf would exceed this limit, the mux releases
  its lock and polls (5ms intervals) until the reader drains the buffer

### 7.2 Rationale

Dropping data would corrupt the TCP stream carried over the mux.
The backpressure mechanism ensures lossless delivery at the cost
of blocking the frame processing goroutine.

**Invariant**: `len(stream.recvBuf) <= maxBufferSize` after every append.

### 7.3 Send Queue

- Send queue is bounded by the underlying transport (DataChannel/VP8)
- No mux-level send backpressure beyond transport errors

## 8. Reconnect Reset Semantics

### 8.1 Full Reset

`Reset()` invalidates ALL mux state:
- All streams marked closed and removed
- Stream ID counter reset to 1
- Send sequence counters cleared

### 8.2 Client Reset

`ResetClient(clientID)` removes only streams belonging to the given client.
Other clients' streams are unaffected.

### 8.3 Send Function Update

`UpdateSendFunc()` allows swapping the transport send function after
reconnect without resetting stream state.

## 9. Invariants Summary

1. Frame header is always 12 bytes
2. Payload length matches the Length field exactly
3. Stream ID 0 is never allocated
4. Stream ID 0xFFFF is reserved for control frames
5. Per-stream sequence numbers are monotonically increasing (sender)
6. Data is delivered in sequence order (receiver)
7. Out-of-order buffer limited to 100 frames per stream
8. Receive buffer limited to 32 MiB per stream
9. Close frame has Length=0, Sequence=0
10. Control frame has StreamID=0xFFFF, Length=0xFFFF
11. Client ID mismatch triggers full stream reset
12. Full Reset clears all state and resets ID counter
