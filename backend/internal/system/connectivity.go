package system

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

var (
	ErrInternetCheckAddressRequired = errors.New("internet check address is required")
	ErrInternetCheckAddressInvalid  = errors.New("internet check address must use host:port format")
	ErrInternetCheckTimeoutInvalid  = errors.New("internet check timeout must be positive")
)

type InternetConnectionStatus string

const (
	InternetConnectionOnline  InternetConnectionStatus = "online"
	InternetConnectionOffline InternetConnectionStatus = "offline"
)

type InternetStatusResult struct {
	Status    InternetConnectionStatus `json:"status"`
	CheckedAt time.Time                `json:"checked_at"`
	LatencyMS *float64                 `json:"latency_ms"`
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type ConnectivityChecker struct {
	address     string
	timeout     time.Duration
	dialContext dialContextFunc
	now         func() time.Time
}

func NewConnectivityChecker(address string, timeout time.Duration) (*ConnectivityChecker, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, ErrInternetCheckAddressRequired
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return nil, ErrInternetCheckAddressInvalid
	}
	if timeout <= 0 {
		return nil, ErrInternetCheckTimeoutInvalid
	}

	dialer := &net.Dialer{}
	return &ConnectivityChecker{
		address:     address,
		timeout:     timeout,
		dialContext: dialer.DialContext,
		now:         time.Now,
	}, nil
}

func (c *ConnectivityChecker) Check(ctx context.Context) InternetStatusResult {
	startedAt := c.now()
	checkContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	connection, err := c.dialContext(checkContext, "tcp", c.address)
	checkedAt := c.now()
	if err != nil || connection == nil {
		return InternetStatusResult{
			Status:    InternetConnectionOffline,
			CheckedAt: checkedAt.UTC(),
		}
	}
	_ = connection.Close()

	latency := float64(checkedAt.Sub(startedAt)) / float64(time.Millisecond)
	return InternetStatusResult{
		Status:    InternetConnectionOnline,
		CheckedAt: checkedAt.UTC(),
		LatencyMS: &latency,
	}
}
