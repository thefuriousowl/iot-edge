//go:build windows

package winservice

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

type memoryEventLog struct {
	mu       sync.Mutex
	messages []string
}

func (l *memoryEventLog) Info(_ uint32, message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
	return nil
}
func (l *memoryEventLog) Error(_ uint32, message string) error { return l.Info(0, message) }
func (l *memoryEventLog) Close() error                         { return nil }

func TestHandlerReportsLifecycleAndCancelsRuntimeOnStop(t *testing.T) {
	requests := make(chan svc.ChangeRequest, 1)
	changes := make(chan svc.Status, 4)
	logger := &memoryEventLog{}
	cancelled := make(chan struct{})
	h := &handler{name: "IoTEdge", log: logger, run: func(ctx context.Context) error { <-ctx.Done(); close(cancelled); return ctx.Err() }}
	done := make(chan struct{})
	go func() { h.Execute(nil, requests, changes); close(done) }()

	if state := (<-changes).State; state != svc.StartPending {
		t.Fatalf("first state = %v", state)
	}
	if status := <-changes; status.State != svc.Running || status.Accepts != svc.AcceptStop|svc.AcceptShutdown {
		t.Fatalf("running status = %#v", status)
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if state := (<-changes).State; state != svc.StopPending {
		t.Fatalf("stop state = %v", state)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("runtime was not cancelled")
	}
	if state := (<-changes).State; state != svc.Stopped {
		t.Fatalf("final state = %v", state)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit")
	}
}
