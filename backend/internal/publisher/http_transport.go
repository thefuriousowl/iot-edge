package publisher

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ErrHTTPTransportRequired = errors.New("HTTP snapshot transport dependencies are required")
	ErrHTTPTransportExited   = errors.New("HTTP snapshot transport exited unexpectedly")
	ErrInvalidHTTPSnapshot   = errors.New("invalid HTTP Publisher snapshot")
)

type HTTPListenFunc func(string, string) (net.Listener, error)

type HTTPTransportOption func(*HTTPTransportFactory) error

func WithHTTPListenFunc(listen HTTPListenFunc) HTTPTransportOption {
	return func(factory *HTTPTransportFactory) error {
		if listen == nil {
			return ErrHTTPTransportRequired
		}
		factory.listen = listen
		return nil
	}
}

type HTTPTransportFactory struct {
	secrets SecretResolver
	listen  HTTPListenFunc
}

func NewHTTPTransportFactory(secrets SecretResolver, options ...HTTPTransportOption) (*HTTPTransportFactory, error) {
	if isNilSourceDependency(secrets) {
		return nil, ErrSecretResolverRequired
	}
	factory := &HTTPTransportFactory{secrets: secrets, listen: net.Listen}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(factory); err != nil {
			return nil, err
		}
	}
	return factory, nil
}

func (factory *HTTPTransportFactory) ListenerClaims(entity Publisher) ([]ListenerClaim, error) {
	config, err := validateHTTPTransportPublisher(entity)
	if err != nil {
		return nil, err
	}
	return []ListenerClaim{{Network: "tcp", Address: net.JoinHostPort(config.HTTP.BindAddress, strconv.Itoa(int(config.HTTP.Port)))}}, nil
}

func (factory *HTTPTransportFactory) NewTransport(ctx context.Context, entity Publisher) (Transport, error) {
	if factory == nil || factory.listen == nil || isNilSourceDependency(factory.secrets) || ctx == nil {
		return nil, ErrHTTPTransportRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := validateHTTPTransportPublisher(entity)
	if err != nil {
		return nil, err
	}
	var apiKey []byte
	if config.HTTP.Access.Mode == HTTPAccessAPIKey {
		material, _, resolveErr := factory.secrets.Resolve(ctx, entity.ID, *config.HTTP.Access.APIKey, SecretKindOpaque)
		if resolveErr != nil {
			return nil, resolveErr
		}
		defer material.Destroy()
		if len(material.Opaque) == 0 {
			return nil, ErrInvalidSecretMaterial
		}
		apiKey = append([]byte(nil), material.Opaque...)
	}
	address := net.JoinHostPort(config.HTTP.BindAddress, strconv.Itoa(int(config.HTTP.Port)))
	listener, err := factory.listen("tcp", address)
	if err != nil {
		zeroBytes(apiKey)
		return nil, err
	}
	transport := &httpSnapshotTransport{
		publisherID: entity.ID, config: config.HTTP, listener: listener, apiKey: apiKey,
		failures: make(chan error, 1), done: make(chan struct{}),
	}
	limitedListener := &httpConnectionLimitListener{
		Listener: listener, permits: make(chan struct{}, config.HTTP.MaxConnections),
		onRejected: func() { transport.rejectedRequests.Add(1) },
	}
	transport.server = &http.Server{
		Handler: http.HandlerFunc(transport.handle), ReadTimeout: time.Duration(config.HTTP.ReadTimeoutMS) * time.Millisecond,
		ReadHeaderTimeout: time.Duration(config.HTTP.ReadTimeoutMS) * time.Millisecond,
		WriteTimeout:      time.Duration(config.HTTP.WriteTimeoutMS) * time.Millisecond,
		IdleTimeout:       time.Duration(config.HTTP.IdleTimeoutMS) * time.Millisecond, MaxHeaderBytes: config.HTTP.MaxHeaderBytes,
		ConnState: transport.connectionState,
	}
	go transport.serve(limitedListener)
	return transport, nil
}

type httpSnapshotTransport struct {
	publisherID uuid.UUID
	config      HTTPServerConfig
	listener    net.Listener
	server      *http.Server
	apiKey      []byte
	failures    chan error
	done        chan struct{}
	closeOnce   sync.Once
	closeErr    error

	snapshotMu sync.RWMutex
	snapshot   *SourceSnapshot

	externalRequests    atomic.Uint64
	rejectedRequests    atomic.Uint64
	activeConnections   atomic.Int64
	lastRequestUnixNano atomic.Int64
}

func (transport *httpSnapshotTransport) Publish(ctx context.Context, snapshot SourceSnapshot) error {
	if transport == nil || ctx == nil || snapshot.CapturedAt.IsZero() || len(snapshot.Samples) == 0 {
		return ErrInvalidHTTPSnapshot
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cloned := cloneSourceSnapshot(snapshot)
	transport.snapshotMu.Lock()
	transport.snapshot = &cloned
	transport.snapshotMu.Unlock()
	return nil
}

func (transport *httpSnapshotTransport) Close(ctx context.Context) error {
	if transport == nil || ctx == nil {
		return ErrInvalidInput
	}
	transport.closeOnce.Do(func() {
		transport.closeErr = transport.server.Shutdown(ctx)
		if errors.Is(transport.closeErr, http.ErrServerClosed) {
			transport.closeErr = nil
		}
		select {
		case <-transport.done:
		case <-ctx.Done():
			if transport.closeErr == nil {
				transport.closeErr = ctx.Err()
			}
		}
		zeroBytes(transport.apiKey)
		transport.apiKey = nil
	})
	return transport.closeErr
}

func (transport *httpSnapshotTransport) Failures() <-chan error {
	if transport == nil {
		return nil
	}
	return transport.failures
}

func (transport *httpSnapshotTransport) Metrics() TransportMetrics {
	if transport == nil {
		return TransportMetrics{}
	}
	metrics := TransportMetrics{
		ExternalRequestCount: transport.externalRequests.Load(), RejectedRequestCount: transport.rejectedRequests.Load(),
	}
	active := transport.activeConnections.Load()
	if active > 0 {
		metrics.ActiveConnections = uint64(active)
	}
	if unixNano := transport.lastRequestUnixNano.Load(); unixNano > 0 {
		requestedAt := time.Unix(0, unixNano).UTC()
		metrics.LastExternalRequestAt = &requestedAt
	}
	return metrics
}

func (transport *httpSnapshotTransport) Address() string {
	if transport == nil || transport.listener == nil {
		return ""
	}
	return transport.listener.Addr().String()
}

func (transport *httpSnapshotTransport) serve(listener net.Listener) {
	defer close(transport.done)
	err := transport.server.Serve(listener)
	if err == nil {
		err = ErrHTTPTransportExited
	}
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return
	}
	select {
	case transport.failures <- fmt.Errorf("%w: %v", ErrHTTPTransportExited, err):
	default:
	}
}

func (transport *httpSnapshotTransport) handle(writer http.ResponseWriter, request *http.Request) {
	transport.externalRequests.Add(1)
	transport.lastRequestUnixNano.Store(time.Now().UTC().UnixNano())
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if transport.config.Access.Mode == HTTPAccessAnonymous {
		writer.Header().Set("Warning", `299 iot-edge "Anonymous Data Publisher endpoint"`)
		writer.Header().Set("X-IoT-Edge-Anonymous", "true")
	}
	if request.URL.Path != transport.config.Path {
		transport.writeError(writer, http.StatusNotFound, "NOT_FOUND", "Snapshot endpoint not found")
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		transport.writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is allowed")
		return
	}
	if request.ContentLength > 0 || len(request.TransferEncoding) > 0 {
		request.Close = true
		transport.writeError(writer, http.StatusRequestEntityTooLarge, "REQUEST_BODY_NOT_ALLOWED", "Snapshot requests must not include a body")
		return
	}
	if transport.config.Access.Mode == HTTPAccessAPIKey && !transport.authorized(request.Header.Get("X-API-Key")) {
		writer.Header().Set("WWW-Authenticate", `ApiKey realm="iot-edge-publisher"`)
		transport.writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "A valid API key is required")
		return
	}
	transport.snapshotMu.RLock()
	if transport.snapshot == nil {
		transport.snapshotMu.RUnlock()
		transport.writeError(writer, http.StatusServiceUnavailable, "SNAPSHOT_UNAVAILABLE", "No Publisher snapshot is available")
		return
	}
	snapshot := cloneSourceSnapshot(*transport.snapshot)
	transport.snapshotMu.RUnlock()
	quality := httpSnapshotQuality(snapshot)
	statusCode := http.StatusOK
	if transport.config.QualityPolicy == HTTPQualityStrict && quality != "good" {
		statusCode = http.StatusServiceUnavailable
	}
	writer.Header().Set("Content-Type", "application/json")
	if statusCode != http.StatusOK {
		transport.rejectedRequests.Add(1)
	}
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(httpSnapshotResponse{
		PublisherID: transport.publisherID, CapturedAt: snapshot.CapturedAt, Quality: quality, Samples: snapshot.Samples,
	})
}

func (transport *httpSnapshotTransport) authorized(presented string) bool {
	presentedHash := sha256.Sum256([]byte(presented))
	expectedHash := sha256.Sum256(transport.apiKey)
	return subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) == 1
}

func (transport *httpSnapshotTransport) writeError(writer http.ResponseWriter, status int, code, message string) {
	transport.rejectedRequests.Add(1)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(httpErrorResponse{Error: httpErrorBody{Code: code, Message: message}})
}

func (transport *httpSnapshotTransport) connectionState(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		transport.activeConnections.Add(1)
	case http.StateHijacked, http.StateClosed:
		transport.activeConnections.Add(-1)
	}
}

type httpSnapshotResponse struct {
	PublisherID uuid.UUID      `json:"publisher_id"`
	CapturedAt  time.Time      `json:"captured_at"`
	Quality     string         `json:"quality"`
	Samples     []SourceSample `json:"samples"`
}

type httpErrorResponse struct {
	Error httpErrorBody `json:"error"`
}

type httpErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func httpSnapshotQuality(snapshot SourceSnapshot) string {
	quality := "good"
	for _, sample := range snapshot.Samples {
		if !sample.Available || sample.Quality == SourceQualityBad || sample.Quality == SourceQualityUnavailable {
			return "unavailable"
		}
		if sample.Quality != SourceQualityGood {
			quality = "degraded"
		}
	}
	return quality
}

func validateHTTPTransportPublisher(entity Publisher) (HTTPPublisherConfig, error) {
	if entity.ID == uuid.Nil || entity.Type != TypeHTTPServer || entity.ConfigVersion != 3 {
		return HTTPPublisherConfig{}, ErrInvalidPublisher
	}
	return ParseHTTPPublisherConfig(entity.Config)
}

type httpConnectionLimitListener struct {
	net.Listener
	permits    chan struct{}
	onRejected func()
}

func (listener *httpConnectionLimitListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case listener.permits <- struct{}{}:
			return &httpLimitedConnection{Conn: connection, release: func() { <-listener.permits }}, nil
		default:
			_ = connection.Close()
			if listener.onRejected != nil {
				listener.onRejected()
			}
		}
	}
}

type httpLimitedConnection struct {
	net.Conn
	releaseOnce sync.Once
	release     func()
}

func (connection *httpLimitedConnection) Close() error {
	err := connection.Conn.Close()
	connection.releaseOnce.Do(connection.release)
	return err
}

var _ TransportFactory = (*HTTPTransportFactory)(nil)
var _ Transport = (*httpSnapshotTransport)(nil)
var _ TransportMetricsProvider = (*httpSnapshotTransport)(nil)
var _ TransportFailureSource = (*httpSnapshotTransport)(nil)
