package publisher

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"
)

var (
	ErrHTTPServerProbeFailed   = errors.New("HTTP Server listener probe failed")
	ErrHTTPServerProbeTimedOut = errors.New("HTTP Server listener probe timed out")
)

const defaultHTTPServerProbeTimeout = 2 * time.Second

type HTTPServerEndpointMetadata struct {
	Network       string            `json:"network"`
	Scheme        string            `json:"scheme"`
	BindAddress   string            `json:"bind_address"`
	Port          uint16            `json:"port"`
	Path          string            `json:"path"`
	AccessMode    HTTPAccessMode    `json:"access_mode"`
	QualityPolicy HTTPQualityPolicy `json:"quality_policy"`
}

type HTTPServerProbeResult struct {
	Reachable bool                       `json:"reachable"`
	ProbedAt  time.Time                  `json:"probed_at"`
	LatencyMS float64                    `json:"latency_ms"`
	Endpoint  HTTPServerEndpointMetadata `json:"endpoint"`
}

type HTTPServerDialFunc func(context.Context, string, string) (net.Conn, error)

type HTTPServerProbeOption func(*HTTPServerProber) error

func WithHTTPServerProbeDialer(dial HTTPServerDialFunc) HTTPServerProbeOption {
	return func(prober *HTTPServerProber) error {
		if dial == nil {
			return ErrHTTPTransportRequired
		}
		prober.dial = dial
		return nil
	}
}

func WithHTTPServerProbeClock(clock func() time.Time) HTTPServerProbeOption {
	return func(prober *HTTPServerProber) error {
		if clock == nil {
			return ErrHTTPTransportRequired
		}
		prober.now = clock
		return nil
	}
}

func WithHTTPServerProbeTimeout(timeout time.Duration) HTTPServerProbeOption {
	return func(prober *HTTPServerProber) error {
		if timeout <= 0 {
			return ErrInvalidInput
		}
		prober.timeout = timeout
		return nil
	}
}

type HTTPServerProber struct {
	dial    HTTPServerDialFunc
	now     func() time.Time
	timeout time.Duration
}

func NewHTTPServerProber(options ...HTTPServerProbeOption) (*HTTPServerProber, error) {
	dialer := &net.Dialer{}
	prober := &HTTPServerProber{
		dial: dialer.DialContext, now: time.Now, timeout: defaultHTTPServerProbeTimeout,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(prober); err != nil {
			return nil, err
		}
	}
	return prober, nil
}

func HTTPServerEndpoint(entity Publisher) (HTTPServerEndpointMetadata, error) {
	config, err := validateHTTPTransportPublisher(entity)
	if err != nil {
		return HTTPServerEndpointMetadata{}, err
	}
	return HTTPServerEndpointMetadata{
		Network: "tcp", Scheme: "http", BindAddress: config.HTTP.BindAddress, Port: config.HTTP.Port,
		Path: config.HTTP.Path, AccessMode: config.HTTP.Access.Mode, QualityPolicy: config.HTTP.QualityPolicy,
	}, nil
}

func (prober *HTTPServerProber) Probe(ctx context.Context, entity Publisher) (HTTPServerProbeResult, error) {
	if prober == nil || prober.dial == nil || prober.now == nil || prober.timeout <= 0 || ctx == nil {
		return HTTPServerProbeResult{}, ErrHTTPTransportRequired
	}
	if err := ctx.Err(); err != nil {
		return HTTPServerProbeResult{}, err
	}
	endpoint, err := HTTPServerEndpoint(entity)
	if err != nil {
		return HTTPServerProbeResult{}, err
	}
	probeHost := endpoint.BindAddress
	if address := net.ParseIP(probeHost); address != nil && address.IsUnspecified() {
		if address.To4() == nil {
			probeHost = "::1"
		} else {
			probeHost = "127.0.0.1"
		}
	}
	probeContext, cancel := context.WithTimeout(ctx, prober.timeout)
	defer cancel()
	startedAt := prober.now().UTC()
	connection, err := prober.dial(probeContext, endpoint.Network, net.JoinHostPort(probeHost, strconv.Itoa(int(endpoint.Port))))
	probedAt := prober.now().UTC()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(probeContext.Err(), context.Canceled) {
			return HTTPServerProbeResult{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return HTTPServerProbeResult{}, ErrHTTPServerProbeTimedOut
		}
		return HTTPServerProbeResult{}, ErrHTTPServerProbeFailed
	}
	if connection == nil {
		return HTTPServerProbeResult{}, ErrHTTPServerProbeFailed
	}
	if err := connection.Close(); err != nil {
		return HTTPServerProbeResult{}, ErrHTTPServerProbeFailed
	}
	latency := probedAt.Sub(startedAt)
	if latency < 0 {
		latency = 0
	}
	return HTTPServerProbeResult{Reachable: true, ProbedAt: probedAt, LatencyMS: float64(latency) / float64(time.Millisecond), Endpoint: endpoint}, nil
}
