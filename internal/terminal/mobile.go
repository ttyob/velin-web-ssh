package terminal

import (
	"crypto/sha256"
	"errors"
	"time"
)

const MobileInputMaxBytes = 48 * 1024
const mobileInputTTL = 10 * time.Minute
const mobileInputCapacity = 512

// SubscribeView permits an explicit later control request without taking control
// merely because a mobile screen was opened or reconnected.
func (s *Session) SubscribeView(clientID, streamID string, offset uint64) (<-chan Event, <-chan struct{}, Replay) {
	return s.subscribe(clientID, streamID, offset, true, false)
}

func (s *Session) geometryLocked() Event {
	rows, cols, version := s.gridRows, s.gridCols, s.gridVersion
	if rows == 0 || cols == 0 {
		rows, cols = 30, 120 // Initial SSH PTY dimensions, before the first resize.
	}
	return Event{Type: "geometry", Rows: rows, Cols: cols, GeometryVersion: version}
}

func (s *Session) Geometry() Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.geometryLocked()
}

type InputAck struct {
	Type      string `json:"type"`
	InputID   string `json:"inputID"`
	Result    string `json:"result"`
	Bytes     int    `json:"bytes"`
	Code      string `json:"code,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

type mobileInputReceipt struct {
	digest  [32]byte
	ack     InputAck
	expires time.Time
}

// WriteAcknowledged serializes inputs with regular Web inputs. Receipts belong
// to one authenticated device and session, survive reconnects, and are bounded.
// They acknowledge SSH writes, never command execution. Callers must not retry
// automatically: expiration and process restarts end the deduplication window.
func (s *Session) WriteAcknowledged(clientID, scope, inputID string, data []byte) InputAck {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	ack := InputAck{Type: "input_ack", InputID: inputID, Result: "rejected"}
	if scope == "" || len(inputID) == 0 || len(inputID) > 80 || len(data) == 0 || len(data) > MobileInputMaxBytes {
		ack.Code = "invalid_input"
		return ack
	}
	now := time.Now()
	for key, item := range s.mobileInputs {
		if !now.Before(item.expires) {
			delete(s.mobileInputs, key)
		}
	}
	key := scope + "\x00" + inputID
	digest := sha256.Sum256(data)
	if item, ok := s.mobileInputs[key]; ok {
		if item.digest != digest {
			ack.Code = "input_id_conflict"
			return ack
		}
		ack = item.ack
		ack.Duplicate = true
		return ack
	}
	if len(s.mobileInputs) >= mobileInputCapacity {
		ack.Code = "input_cache_full"
		return ack
	}
	if !s.IsController(clientID) {
		ack.Code = "not_controller"
	} else {
		n, err := s.writeBytes(clientID, data)
		ack.Bytes = n
		switch {
		case err == nil:
			ack.Result = "written"
		case errors.Is(err, ErrNotController):
			ack.Code = "not_controller"
		default:
			ack.Result, ack.Code = "uncertain", "write_failed"
		}
	}
	if s.mobileInputs == nil {
		s.mobileInputs = make(map[string]mobileInputReceipt)
	}
	s.mobileInputs[key] = mobileInputReceipt{digest: digest, ack: ack, expires: now.Add(mobileInputTTL)}
	return ack
}
