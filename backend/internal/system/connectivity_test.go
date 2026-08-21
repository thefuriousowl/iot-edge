package system

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestNewConnectivityCheckerValidatesConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		timeout time.Duration
		wantErr error
	}{
		{name: "empty address", timeout: time.Second, wantErr: ErrInternetCheckAddressRequired},
		{name: "missing port", address: "example.com", timeout: time.Second, wantErr: ErrInternetCheckAddressInvalid},
		{name: "empty host", address: ":443", timeout: time.Second, wantErr: ErrInternetCheckAddressInvalid},
		{name: "empty port", address: "example.com:", timeout: time.Second, wantErr: ErrInternetCheckAddressInvalid},
		{name: "invalid timeout", address: "example.com:443", wantErr: ErrInternetCheckTimeoutInvalid},
		{name: "valid hostname", address: "example.com:443", timeout: time.Second},
		{name: "valid ipv6", address: "[2001:db8::1]:443", timeout: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checker, err := NewConnectivityChecker(tt.address, tt.timeout)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewConnectivityChecker() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && checker != nil {
				t.Fatalf("NewConnectivityChecker() = %#v, want nil", checker)
			}
			if tt.wantErr == nil && checker == nil {
				t.Fatal("NewConnectivityChecker() = nil, want checker")
			}
		})
	}
}

func TestConnectivityCheckerCheckReturnsOnlineWithFractionalLatency(t *testing.T) {
	t.Parallel()

	checker, err := NewConnectivityChecker("example.com:443", time.Second)
	if err != nil {
		t.Fatalf("NewConnectivityChecker() error = %v", err)
	}
	client, server := net.Pipe()
	defer server.Close()

	checker.dialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "example.com:443" {
			t.Fatalf("dial = %s %s, want tcp example.com:443", network, address)
		}
		return client, nil
	}
	times := []time.Time{
		time.Date(2026, 8, 21, 13, 0, 0, 0, time.FixedZone("ICT", 7*60*60)),
		time.Date(2026, 8, 21, 13, 0, 0, 12_500_000, time.FixedZone("ICT", 7*60*60)),
	}
	checker.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	result := checker.Check(context.Background())
	if result.Status != InternetConnectionOnline {
		t.Errorf("status = %q, want %q", result.Status, InternetConnectionOnline)
	}
	if result.CheckedAt.Location() != time.UTC || result.CheckedAt.Format(time.RFC3339Nano) != "2026-08-21T06:00:00.0125Z" {
		t.Errorf("checked_at = %s, want normalized UTC timestamp", result.CheckedAt.Format(time.RFC3339Nano))
	}
	if result.LatencyMS == nil || *result.LatencyMS != 12.5 {
		t.Errorf("latency_ms = %v, want 12.5", result.LatencyMS)
	}

	buffer := make([]byte, 1)
	readResult := make(chan error, 1)
	go func() {
		_, err := server.Read(buffer)
		readResult <- err
	}()
	select {
	case err := <-readResult:
		if !errors.Is(err, io.EOF) {
			t.Errorf("successful probe close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("successful probe connection remains open")
	}
}

func TestConnectivityCheckerCheckReturnsOfflineWithoutLeakingDialError(t *testing.T) {
	t.Parallel()

	checker, err := NewConnectivityChecker("example.com:443", time.Second)
	if err != nil {
		t.Fatalf("NewConnectivityChecker() error = %v", err)
	}
	checkedAt := time.Date(2026, 8, 21, 6, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return checkedAt }
	checker.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("private network detail")
	}

	result := checker.Check(context.Background())
	if result.Status != InternetConnectionOffline {
		t.Errorf("status = %q, want %q", result.Status, InternetConnectionOffline)
	}
	if result.CheckedAt != checkedAt {
		t.Errorf("checked_at = %v, want %v", result.CheckedAt, checkedAt)
	}
	if result.LatencyMS != nil {
		t.Errorf("latency_ms = %v, want nil", result.LatencyMS)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(payload) != `{"status":"offline","checked_at":"2026-08-21T06:00:00Z","latency_ms":null}` {
		t.Errorf("JSON = %s, want stable sanitized contract", payload)
	}
}

func TestConnectivityCheckerCheckTreatsNilConnectionAsOffline(t *testing.T) {
	t.Parallel()

	checker, err := NewConnectivityChecker("example.com:443", time.Second)
	if err != nil {
		t.Fatalf("NewConnectivityChecker() error = %v", err)
	}
	checker.dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, nil
	}

	result := checker.Check(context.Background())
	if result.Status != InternetConnectionOffline || result.LatencyMS != nil {
		t.Errorf("Check() = %#v, want offline without latency", result)
	}
}

func TestConnectivityCheckerCheckHonorsTimeoutAndParentCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout time.Duration
		context func() context.Context
	}{
		{
			name:    "checker timeout",
			timeout: 5 * time.Millisecond,
			context: context.Background,
		},
		{
			name:    "parent cancellation",
			timeout: time.Second,
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checker, err := NewConnectivityChecker("example.com:443", tt.timeout)
			if err != nil {
				t.Fatalf("NewConnectivityChecker() error = %v", err)
			}
			checker.dialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}

			result := checker.Check(tt.context())
			if result.Status != InternetConnectionOffline || result.LatencyMS != nil {
				t.Errorf("Check() = %#v, want offline without latency", result)
			}
		})
	}
}
