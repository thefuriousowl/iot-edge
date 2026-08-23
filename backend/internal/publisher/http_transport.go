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
	"strings"
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

func WithHTTPClock(clock func() time.Time) HTTPTransportOption {
	return func(factory *HTTPTransportFactory) error {
		if clock == nil {
			return ErrHTTPTransportRequired
		}
		factory.now = clock
		return nil
	}
}

type HTTPTransportFactory struct {
	secrets SecretResolver
	engine  *JSONPayloadEngine
	listen  HTTPListenFunc
	now     func() time.Time
}

func NewHTTPTransportFactory(secrets SecretResolver, engine *JSONPayloadEngine, options ...HTTPTransportOption) (*HTTPTransportFactory, error) {
	if isNilSourceDependency(secrets) {
		return nil, ErrSecretResolverRequired
	}
	if engine == nil {
		return nil, ErrHTTPTransportRequired
	}
	factory := &HTTPTransportFactory{secrets: secrets, engine: engine, listen: net.Listen, now: time.Now}
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

func (factory *HTTPTransportFactory) NewTransport(context.Context, Publisher) (Transport, error) {
	return nil, ErrHTTPTransportRequired
}

func (factory *HTTPTransportFactory) NewResolvedTransport(ctx context.Context, entity Publisher, sources []ResolvedSource) (Transport, error) {
	if factory == nil || factory.engine == nil || factory.listen == nil || factory.now == nil || isNilSourceDependency(factory.secrets) || ctx == nil {
		return nil, ErrHTTPTransportRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := validateHTTPTransportPublisher(entity)
	if err != nil {
		return nil, err
	}
	compiled, err := factory.engine.Compile(config.Response.PayloadTemplate, sources)
	if err != nil {
		return nil, err
	}
	access, err := resolveHTTPAccessVerifier(ctx, factory.secrets, entity.ID, config.HTTP.Access)
	if err != nil {
		return nil, err
	}
	address := net.JoinHostPort(config.HTTP.BindAddress, strconv.Itoa(int(config.HTTP.Port)))
	listener, err := factory.listen("tcp", address)
	if err != nil {
		access.clear()
		return nil, err
	}
	transport := &httpSnapshotTransport{
		publisherID: entity.ID, config: config.HTTP, compiled: compiled, listener: listener, access: access, now: factory.now,
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
	compiled    *CompiledJSONPayload
	listener    net.Listener
	server      *http.Server
	access      httpAccessVerifier
	now         func() time.Time
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
		transport.access.clear()
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
	if !transport.authorized(request) {
		transport.setAuthenticateHeader(writer)
		transport.writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED", "Valid Publisher credentials are required")
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
	payload, err := transport.compiled.RenderWithContext(JSONPayloadRenderContext{
		PublisherID: transport.publisherID, PublishedAt: transport.now().UTC(), Snapshot: snapshot,
	})
	if err != nil {
		transport.writeError(writer, http.StatusServiceUnavailable, "PAYLOAD_RENDER_FAILED", "Publisher payload could not be rendered")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if statusCode != http.StatusOK {
		transport.rejectedRequests.Add(1)
	}
	writer.WriteHeader(statusCode)
	_, _ = writer.Write(payload)
}

func (transport *httpSnapshotTransport) authorized(request *http.Request) bool {
	switch transport.access.mode {
	case HTTPAccessAnonymous:
		return true
	case HTTPAccessAPIKey:
		return transport.access.matches(transport.access.apiKeyHash, request.Header.Get(transport.access.apiKeyHeader))
	case HTTPAccessBasic:
		username, password, ok := request.BasicAuth()
		return ok && transport.access.matches(transport.access.usernameHash, username) && transport.access.matches(transport.access.passwordHash, password)
	case HTTPAccessBearer:
		parts := strings.Fields(request.Header.Get("Authorization"))
		return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && transport.access.matches(transport.access.bearerHash, parts[1])
	default:
		return false
	}
}

func (transport *httpSnapshotTransport) setAuthenticateHeader(writer http.ResponseWriter) {
	switch transport.access.mode {
	case HTTPAccessAPIKey:
		writer.Header().Set("WWW-Authenticate", `ApiKey realm="iot-edge-publisher"`)
	case HTTPAccessBasic:
		writer.Header().Set("WWW-Authenticate", `Basic realm="iot-edge-publisher", charset="UTF-8"`)
	case HTTPAccessBearer:
		writer.Header().Set("WWW-Authenticate", `Bearer realm="iot-edge-publisher"`)
	}
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
	if entity.ID == uuid.Nil || entity.Type != TypeHTTPServer || entity.ConfigVersion != 4 {
		return HTTPPublisherConfig{}, ErrInvalidPublisher
	}
	return ParseHTTPPublisherConfig(entity.Config)
}

type httpAccessVerifier struct {
	mode         HTTPAccessMode
	apiKeyHeader string
	apiKeyHash   [sha256.Size]byte
	usernameHash [sha256.Size]byte
	passwordHash [sha256.Size]byte
	bearerHash   [sha256.Size]byte
}

func resolveHTTPAccessVerifier(ctx context.Context, resolver SecretResolver, publisherID uuid.UUID, config HTTPAccessConfig) (httpAccessVerifier, error) {
	verifier := httpAccessVerifier{mode: config.Mode, apiKeyHeader: config.APIKeyHeader}
	var err error
	switch config.Mode {
	case HTTPAccessAnonymous:
		return verifier, nil
	case HTTPAccessAPIKey:
		verifier.apiKeyHash, err = resolveHTTPOpaqueHash(ctx, resolver, publisherID, "http.api_key")
	case HTTPAccessBasic:
		verifier.usernameHash, err = resolveHTTPOpaqueHash(ctx, resolver, publisherID, "http.username")
		if err == nil {
			verifier.passwordHash, err = resolveHTTPOpaqueHash(ctx, resolver, publisherID, "http.password")
		}
	case HTTPAccessBearer:
		verifier.bearerHash, err = resolveHTTPOpaqueHash(ctx, resolver, publisherID, "http.bearer_token")
	default:
		err = ErrInvalidPublisherConfig
	}
	if err != nil {
		verifier.clear()
	}
	return verifier, err
}

func resolveHTTPOpaqueHash(ctx context.Context, resolver SecretResolver, publisherID uuid.UUID, name string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	material, _, err := resolver.Resolve(ctx, publisherID, SecretReference{Name: name}, SecretKindOpaque)
	if err != nil {
		return zero, err
	}
	defer material.Destroy()
	if len(material.Opaque) == 0 {
		return zero, ErrInvalidSecretMaterial
	}
	return sha256.Sum256(material.Opaque), nil
}

func (verifier *httpAccessVerifier) matches(expected [sha256.Size]byte, presented string) bool {
	presentedHash := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(expected[:], presentedHash[:]) == 1
}

func (verifier *httpAccessVerifier) clear() {
	if verifier == nil {
		return
	}
	zeroBytes(verifier.apiKeyHash[:])
	zeroBytes(verifier.usernameHash[:])
	zeroBytes(verifier.passwordHash[:])
	zeroBytes(verifier.bearerHash[:])
	verifier.apiKeyHeader = ""
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
var _ ResolvedTransportFactory = (*HTTPTransportFactory)(nil)
var _ Transport = (*httpSnapshotTransport)(nil)
var _ TransportMetricsProvider = (*httpSnapshotTransport)(nil)
var _ TransportFailureSource = (*httpSnapshotTransport)(nil)
