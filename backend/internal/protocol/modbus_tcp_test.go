package protocol

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aldas/go-modbus-client/packet"
)

func TestNewModbusTCPClient(t *testing.T) {
	t.Parallel()

	t.Run("builds address and timeout from valid config", func(t *testing.T) {
		t.Parallel()

		client, err := newModbusTCPClient(validModbusTCPConfig(), nil)
		if err != nil {
			t.Fatalf("newModbusTCPClient() error = %v", err)
		}
		if client.address != "192.0.2.10:502" {
			t.Fatalf("address = %q, want %q", client.address, "192.0.2.10:502")
		}
		if client.timeout != 5*time.Second {
			t.Fatalf("timeout = %v, want %v", client.timeout, 5*time.Second)
		}
		if client.transport == nil {
			t.Fatal("transport = nil, want configured transport")
		}
		if client.IsConnected() {
			t.Fatal("new client is connected, want disconnected")
		}
	})

	t.Run("formats IPv6 address safely", func(t *testing.T) {
		t.Parallel()

		config := validModbusTCPConfig()
		config.Host = "2001:db8::10"

		client, err := newModbusTCPClient(config, nil)
		if err != nil {
			t.Fatalf("newModbusTCPClient() error = %v", err)
		}
		if client.address != "[2001:db8::10]:502" {
			t.Fatalf("address = %q, want %q", client.address, "[2001:db8::10]:502")
		}
	})

	t.Run("returns public interface", func(t *testing.T) {
		t.Parallel()

		client, err := NewModbusTCPClient(validModbusTCPConfig())
		if err != nil {
			t.Fatalf("NewModbusTCPClient() error = %v", err)
		}
		if client == nil {
			t.Fatal("NewModbusTCPClient() = nil, want client")
		}
	})
}

func TestNewModbusTCPClientRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*ModbusTCPConfig)
		wantDetail string
	}{
		{
			name:       "empty host",
			mutate:     func(config *ModbusTCPConfig) { config.Host = "" },
			wantDetail: "host is required",
		},
		{
			name:       "whitespace host",
			mutate:     func(config *ModbusTCPConfig) { config.Host = "  \t" },
			wantDetail: "host is required",
		},
		{
			name:       "zero port",
			mutate:     func(config *ModbusTCPConfig) { config.Port = 0 },
			wantDetail: "port must be between 1 and 65535",
		},
		{
			name:       "port above maximum",
			mutate:     func(config *ModbusTCPConfig) { config.Port = 65536 },
			wantDetail: "port must be between 1 and 65535",
		},
		{
			name:       "timeout below minimum",
			mutate:     func(config *ModbusTCPConfig) { config.Timeout = 99 },
			wantDetail: "timeout must be between 100 and 60000 milliseconds",
		},
		{
			name:       "timeout above maximum",
			mutate:     func(config *ModbusTCPConfig) { config.Timeout = 60001 },
			wantDetail: "timeout must be between 100 and 60000 milliseconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := validModbusTCPConfig()
			tt.mutate(&config)

			client, err := newModbusTCPClient(config, nil)
			if client != nil {
				t.Fatalf("newModbusTCPClient() client = %#v, want nil", client)
			}
			if !errors.Is(err, ErrInvalidModbusConfig) {
				t.Fatalf("error = %v, want ErrInvalidModbusConfig", err)
			}
			if !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("error = %q, want detail %q", err, tt.wantDetail)
			}
		})
	}
}

func TestNewModbusTCPClientAcceptsConfigBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		port    int
		timeout int
	}{
		{name: "minimum values", port: 1, timeout: 100},
		{name: "maximum values", port: 65535, timeout: 60000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := validModbusTCPConfig()
			config.Port = tt.port
			config.Timeout = tt.timeout

			client, err := newModbusTCPClient(config, nil)
			if err != nil {
				t.Fatalf("newModbusTCPClient() error = %v", err)
			}
			if client.timeout != time.Duration(tt.timeout)*time.Millisecond {
				t.Fatalf("timeout = %v, want %v", client.timeout, time.Duration(tt.timeout)*time.Millisecond)
			}
		})
	}
}

func TestModbusTCPClientInjectedDialer(t *testing.T) {
	t.Parallel()

	config := validModbusTCPConfig()
	config.Timeout = 250

	var (
		gotAddress  string
		gotDeadline time.Time
		serverConn  net.Conn
	)
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		gotAddress = address
		gotDeadline, _ = ctx.Deadline()
		clientConn, peer := net.Pipe()
		serverConn = peer
		return clientConn, nil
	}

	client, err := newModbusTCPClient(config, dial)
	if err != nil {
		t.Fatalf("newModbusTCPClient() error = %v", err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() {
		if serverConn != nil {
			_ = serverConn.Close()
		}
	})

	if gotAddress != "192.0.2.10:502" {
		t.Fatalf("dial address = %q, want %q", gotAddress, "192.0.2.10:502")
	}
	if gotDeadline.IsZero() {
		t.Fatal("dial context has no deadline")
	}
	if remaining := time.Until(gotDeadline); remaining <= 0 || remaining > 250*time.Millisecond {
		t.Fatalf("dial deadline remaining = %v, want within (0, 250ms]", remaining)
	}
	if err := client.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
}

func TestModbusTCPClientLifecycleIsIdempotent(t *testing.T) {
	t.Parallel()

	transport := &fakeModbusTransport{}
	client := newTestModbusTCPClient(transport)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("first Connect() error = %v", err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if !client.IsConnected() {
		t.Fatal("IsConnected() = false after Connect(), want true")
	}
	if got := transport.connectCallCount(); got != 1 {
		t.Fatalf("transport Connect() calls = %d, want 1", got)
	}

	if err := client.Disconnect(); err != nil {
		t.Fatalf("first Disconnect() error = %v", err)
	}
	if err := client.Disconnect(); err != nil {
		t.Fatalf("second Disconnect() error = %v", err)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after Disconnect(), want false")
	}
	if got := transport.closeCallCount(); got != 1 {
		t.Fatalf("transport Close() calls = %d, want 1", got)
	}
}

func TestModbusTCPClientConnectFailure(t *testing.T) {
	t.Parallel()

	connectErr := errors.New("dial refused")
	transport := &fakeModbusTransport{
		connectFunc: func(context.Context, string) error { return connectErr },
	}
	client := newTestModbusTCPClient(transport)

	err := client.Connect(context.Background())
	if !errors.Is(err, connectErr) {
		t.Fatalf("Connect() error = %v, want wrapped %v", err, connectErr)
	}
	if !strings.Contains(err.Error(), client.address) {
		t.Fatalf("Connect() error = %q, want address %q", err, client.address)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after failed Connect(), want false")
	}
}

func TestModbusTCPClientDisconnectFailureClearsState(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	transport := &fakeModbusTransport{
		closeFunc: func() error { return closeErr },
	}
	client := newTestModbusTCPClient(transport)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	err := client.Disconnect()
	if !errors.Is(err, closeErr) {
		t.Fatalf("Disconnect() error = %v, want wrapped %v", err, closeErr)
	}
	if !strings.Contains(err.Error(), client.address) {
		t.Fatalf("Disconnect() error = %q, want address %q", err, client.address)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after failed Disconnect(), want false")
	}
}

func TestModbusTCPClientConnectHonorsTimeout(t *testing.T) {
	t.Parallel()

	transport := &fakeModbusTransport{
		connectFunc: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	client := newTestModbusTCPClient(transport)
	client.timeout = 20 * time.Millisecond

	started := time.Now()
	err := client.Connect(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Connect() took %v, want less than 1s", elapsed)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after timeout, want false")
	}
}

func TestModbusTCPClientConcurrentLifecycle(t *testing.T) {
	transport := &fakeModbusTransport{}
	client := newTestModbusTCPClient(transport)

	runConcurrently(t, 32, func() error {
		return client.Connect(context.Background())
	})
	if got := transport.connectCallCount(); got != 1 {
		t.Fatalf("concurrent transport Connect() calls = %d, want 1", got)
	}
	if !client.IsConnected() {
		t.Fatal("IsConnected() = false after concurrent Connect(), want true")
	}

	runConcurrently(t, 32, client.Disconnect)
	if got := transport.closeCallCount(); got != 1 {
		t.Fatalf("concurrent transport Close() calls = %d, want 1", got)
	}
	if client.IsConnected() {
		t.Fatal("IsConnected() = true after concurrent Disconnect(), want false")
	}
}

func validModbusTCPConfig() ModbusTCPConfig {
	return ModbusTCPConfig{
		Host:      "192.0.2.10",
		Port:      502,
		Timeout:   5000,
		KeepAlive: true,
	}
}

func newTestModbusTCPClient(transport modbusTransport) *modbusTCPClient {
	return &modbusTCPClient{
		address:   "192.0.2.10:502",
		timeout:   time.Second,
		transport: transport,
	}
}

type fakeModbusTransport struct {
	mu           sync.Mutex
	connectCalls int
	closeCalls   int
	doCalls      int
	connectFunc  func(context.Context, string) error
	closeFunc    func() error
	doFunc       func(context.Context, packet.Request) (packet.Response, error)
}

func (t *fakeModbusTransport) Connect(ctx context.Context, address string) error {
	t.mu.Lock()
	t.connectCalls++
	connectFunc := t.connectFunc
	t.mu.Unlock()

	if connectFunc != nil {
		return connectFunc(ctx, address)
	}
	return nil
}

func (t *fakeModbusTransport) Close() error {
	t.mu.Lock()
	t.closeCalls++
	closeFunc := t.closeFunc
	t.mu.Unlock()

	if closeFunc != nil {
		return closeFunc()
	}
	return nil
}

func (t *fakeModbusTransport) Do(ctx context.Context, request packet.Request) (packet.Response, error) {
	t.mu.Lock()
	t.doCalls++
	doFunc := t.doFunc
	t.mu.Unlock()

	if doFunc != nil {
		return doFunc(ctx, request)
	}
	return nil, nil
}

func (t *fakeModbusTransport) connectCallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connectCalls
}

func (t *fakeModbusTransport) closeCallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeCalls
}

func (t *fakeModbusTransport) doCallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.doCalls
}

func runConcurrently(t *testing.T, count int, operation func() error) {
	t.Helper()

	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- operation()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent operation error = %v", err)
		}
	}
}
