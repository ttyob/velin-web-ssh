package terminal

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

type mobileWriter struct {
	bytes.Buffer
	partial bool
}

func (w *mobileWriter) Close() error { return nil }
func (w *mobileWriter) Write(p []byte) (int, error) {
	if w.partial {
		_, _ = w.Buffer.Write(p[:1])
		return 1, io.ErrUnexpectedEOF
	}
	return w.Buffer.Write(p)
}

func mobileSession() (*Session, *mobileWriter) {
	w := &mobileWriter{}
	s := &Session{subs: make(map[string]*terminalSubscriber), buffer: newRingBuffer(1024), stdin: w}
	return s, w
}

func TestMobileViewNeverAcquiresControlOnAttach(t *testing.T) {
	s, _ := mobileSession()
	s.SubscribeView("phone", "", 0)
	if s.IsController("phone") {
		t.Fatal("opening phone acquired idle control")
	}
	if !s.RequestControl("phone") {
		t.Fatal("explicit control request failed")
	}
	s.SubscribeView("reconnected-phone", "", 0)
	if !s.IsController("phone") || s.IsController("reconnected-phone") {
		t.Fatal("reconnect stole control")
	}
	if s.RequestControl("reconnected-phone") {
		t.Fatal("controller was replaced without approval")
	}
	if !s.RespondControl("phone", "reconnected-phone", true) {
		t.Fatal("approval failed")
	}
}

func TestMobileInputAcknowledgesAndDeduplicatesAcrossConnections(t *testing.T) {
	s, w := mobileSession()
	s.SubscribeView("phone", "", 0)
	s.RequestControl("phone")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ack := s.WriteAcknowledged("phone", "auth:device", "one", []byte("hello\r"))
			if ack.Result != "written" || ack.Bytes != 6 {
				t.Errorf("ack=%+v", ack)
			}
		}()
	}
	wg.Wait()
	if w.String() != "hello\r" {
		t.Fatalf("duplicate writes: %q", w.String())
	}
	s.Unsubscribe("phone")
	s.SubscribeView("next", "", 0)
	ack := s.WriteAcknowledged("next", "auth:device", "one", []byte("hello\r"))
	if !ack.Duplicate || ack.Result != "written" {
		t.Fatalf("lost receipt after reconnect: %+v", ack)
	}
	conflict := s.WriteAcknowledged("next", "auth:device", "one", []byte("changed"))
	if conflict.Code != "input_id_conflict" || w.String() != "hello\r" {
		t.Fatalf("conflict: %+v", conflict)
	}
}

func TestMobileInputRejectsViewerAndPreservesPartialWrite(t *testing.T) {
	s, w := mobileSession()
	s.SubscribeView("phone", "", 0)
	if ack := s.WriteAcknowledged("phone", "device", "one", []byte("abc")); ack.Code != "not_controller" || w.Len() != 0 {
		t.Fatalf("viewer wrote: %+v", ack)
	}
	s.RequestControl("phone")
	w.partial = true
	ack := s.WriteAcknowledged("phone", "device", "two", []byte("abc"))
	if ack.Result != "uncertain" || ack.Bytes != 1 {
		t.Fatalf("partial write: %+v", ack)
	}
	ack = s.WriteAcknowledged("phone", "device", "two", []byte("abc"))
	if !ack.Duplicate || w.String() != "a" {
		t.Fatalf("partial write resent: %+v / %q", ack, w.String())
	}
}

func TestMobileInputCapacityDoesNotEvictLiveReceipts(t *testing.T) {
	s, w := mobileSession()
	s.Subscribe("phone", "", 0)
	s.mobileInputs = make(map[string]mobileInputReceipt)
	for i := 0; i < mobileInputCapacity; i++ {
		s.mobileInputs[string(rune(i))] = mobileInputReceipt{expires: time.Now().Add(time.Hour)}
	}
	if ack := s.WriteAcknowledged("phone", "device", "one", []byte("abc")); ack.Code != "input_cache_full" || w.Len() != 0 {
		t.Fatalf("capacity: %+v", ack)
	}
	s.mobileInputs["expired"] = mobileInputReceipt{expires: time.Now().Add(-time.Second)}
	delete(s.mobileInputs, string(rune(0)))
	if ack := s.WriteAcknowledged("phone", "device", "one", []byte("abc")); ack.Result != "written" {
		t.Fatalf("expiry sweep: %+v", ack)
	}
}

func TestMobileGeometryHasInitialPTYAndCurrentDimensions(t *testing.T) {
	s, _ := mobileSession()
	initial := s.Geometry()
	if initial.Rows != 30 || initial.Cols != 120 {
		t.Fatalf("initial geometry: %+v", initial)
	}
	s.gridRows, s.gridCols, s.gridVersion = 44, 88, 3
	if e := s.Geometry(); e.Rows != 44 || e.Cols != 88 || e.GeometryVersion != 3 {
		t.Fatalf("geometry: %+v", e)
	}
}

type blockedMobileWriter struct {
	started chan struct{}
	release chan struct{}
}

func (w *blockedMobileWriter) Close() error { return nil }
func (w *blockedMobileWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}

func TestMobileBlockedWriteDoesNotBlockControlRelease(t *testing.T) {
	s, _ := mobileSession()
	w := &blockedMobileWriter{started: make(chan struct{}), release: make(chan struct{})}
	s.stdin = w
	s.SubscribeView("phone", "", 0)
	s.RequestControl("phone")
	written := make(chan InputAck, 1)
	go func() { written <- s.WriteAcknowledged("phone", "device", "one", []byte("hello")) }()
	<-w.started
	released := make(chan bool, 1)
	go func() { released <- s.ReleaseControl("phone") }()
	select {
	case ok := <-released:
		if !ok {
			t.Fatal("control was not released")
		}
	case <-time.After(time.Second):
		close(w.release)
		t.Fatal("SSH write blocked the session lock")
	}
	close(w.release)
	if ack := <-written; ack.Result != "written" {
		t.Fatalf("ack: %+v", ack)
	}
}
