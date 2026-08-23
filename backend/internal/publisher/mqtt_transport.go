package publisher

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

const maxMQTTDiagnosticPayloadBytes = 64 * 1024

var (
	ErrMQTTTransportRequired = errors.New("MQTT transport dependencies are required")
	ErrInvalidMQTTPublisher  = errors.New("invalid MQTT Publisher")
	ErrMQTTTransportClosed   = errors.New("MQTT transport is closed")
)

type MQTTDiagnosticEvent struct {
	Sequence   uint64    `json:"sequence"`
	Label      string    `json:"label"`
	Topic      string    `json:"topic"`
	QoS        byte      `json:"qos"`
	Retained   bool      `json:"retained"`
	Duplicate  bool      `json:"duplicate"`
	ReceivedAt time.Time `json:"received_at"`
	Format     string    `json:"format"`
	Payload    string    `json:"payload"`
	Truncated  bool      `json:"truncated"`
}

type mqttWireMessage struct {
	Topic     string
	QoS       byte
	Retained  bool
	Duplicate bool
	Payload   []byte
}

type mqttWireToken interface {
	Done() <-chan struct{}
	Error() error
}

type mqttWireClient interface {
	Connect() mqttWireToken
	Publish(string, byte, bool, []byte) mqttWireToken
	Subscribe(map[string]byte, func(mqttWireMessage)) mqttWireToken
	Disconnect(uint)
	IsConnectionOpen() bool
}

type mqttWireSettings struct {
	BrokerURI        string
	ClientID         string
	Username         string
	Password         string
	TLS              *tls.Config
	KeepAlive        time.Duration
	ConnectTimeout   time.Duration
	WriteTimeout     time.Duration
	ReconnectMin     time.Duration
	ReconnectMax     time.Duration
	OnConnected      func()
	OnConnectionLost func(error)
	OnReconnecting   func()
	DefaultMessage   func(mqttWireMessage)
}

type mqttWireFactory func(mqttWireSettings) (mqttWireClient, error)

type MQTTTransportOption func(*MQTTTransportFactory) error

func WithMQTTWireFactory(factory mqttWireFactory) MQTTTransportOption {
	return func(transportFactory *MQTTTransportFactory) error {
		if factory == nil {
			return ErrMQTTTransportRequired
		}
		transportFactory.newWire = factory
		return nil
	}
}

func WithMQTTClock(clock func() time.Time) MQTTTransportOption {
	return func(factory *MQTTTransportFactory) error {
		if clock == nil {
			return ErrMQTTTransportRequired
		}
		factory.now = clock
		return nil
	}
}

type MQTTTransportFactory struct {
	secrets SecretResolver
	engine  *JSONPayloadEngine
	newWire mqttWireFactory
	now     func() time.Time
}

func NewMQTTTransportFactory(secrets SecretResolver, engine *JSONPayloadEngine, options ...MQTTTransportOption) (*MQTTTransportFactory, error) {
	if isNilSourceDependency(secrets) {
		return nil, ErrSecretResolverRequired
	}
	if engine == nil {
		return nil, ErrMQTTTransportRequired
	}
	factory := &MQTTTransportFactory{secrets: secrets, engine: engine, newWire: newPahoMQTTWireClient, now: time.Now}
	for _, option := range options {
		if option != nil {
			if err := option(factory); err != nil {
				return nil, err
			}
		}
	}
	return factory, nil
}

func (factory *MQTTTransportFactory) ListenerClaims(Publisher) ([]ListenerClaim, error) {
	return []ListenerClaim{}, nil
}

func (factory *MQTTTransportFactory) NewTransport(context.Context, Publisher) (Transport, error) {
	return nil, ErrMQTTTransportRequired
}

func (factory *MQTTTransportFactory) NewResolvedTransport(ctx context.Context, entity Publisher, sources []ResolvedSource) (Transport, error) {
	if factory == nil || factory.engine == nil || factory.newWire == nil || factory.now == nil || isNilSourceDependency(factory.secrets) || ctx == nil {
		return nil, ErrMQTTTransportRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := validateMQTTTransportPublisher(entity)
	if err != nil {
		return nil, err
	}
	compiled, err := factory.engine.Compile(config.MQTT.Publish.PayloadTemplate, sources)
	if err != nil {
		return nil, err
	}
	username, err := resolveMQTTOpaqueSecret(ctx, factory.secrets, entity.ID, config.MQTT.Auth.Username)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(username)
	password, err := resolveMQTTOpaqueSecret(ctx, factory.secrets, entity.ID, config.MQTT.Auth.Password)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(password)
	var tlsConfig *tls.Config
	if strings.HasPrefix(config.MQTT.BrokerURL, "mqtts://") {
		tlsConfig, err = BuildClientTLSConfig(ctx, entity.ID, factory.secrets, config.MQTT.TLS, mqttBrokerHost(config.MQTT.BrokerURL))
		if err != nil {
			return nil, err
		}
	}
	clientID := config.MQTT.ClientID
	if clientID == "" {
		clientID = "iot-edge-" + entity.ID.String()
	}
	transport := &mqttTransport{
		publisherID: entity.ID, config: config.MQTT, compiled: compiled, now: factory.now,
		queue: newMQTTMessageQueue(config.MQTT.QueueCapacity), diagnostics: make([]MQTTDiagnosticEvent, 0, config.MQTT.DiagnosticHistoryDepth),
		stop: make(chan struct{}), done: make(chan struct{}), failures: make(chan error, 1), ready: make(chan struct{}, 1),
	}
	settings := mqttWireSettings{
		BrokerURI: mqttPahoBrokerURI(config.MQTT.BrokerURL), ClientID: clientID, Username: string(username), Password: string(password), TLS: tlsConfig,
		KeepAlive: time.Duration(config.MQTT.KeepAliveMS) * time.Millisecond, ConnectTimeout: time.Duration(config.MQTT.ConnectTimeoutMS) * time.Millisecond,
		WriteTimeout: time.Duration(config.MQTT.PublishTimeoutMS) * time.Millisecond, ReconnectMin: time.Duration(config.MQTT.ReconnectMinMS) * time.Millisecond,
		ReconnectMax: time.Duration(config.MQTT.ReconnectMaxMS) * time.Millisecond,
		OnConnected:  transport.onConnected, OnConnectionLost: transport.onConnectionLost, OnReconnecting: transport.onReconnecting,
		DefaultMessage: transport.onDiagnostic,
	}
	transport.client, err = factory.newWire(settings)
	if err != nil || transport.client == nil {
		return nil, ErrMQTTTransportRequired
	}
	go transport.run()
	return transport, nil
}

type mqttOutboundMessage struct {
	payload []byte
}

type mqttTransport struct {
	publisherID uuid.UUID
	config      MQTTTransportConfig
	compiled    *CompiledJSONPayload
	client      mqttWireClient
	now         func() time.Time
	queue       *mqttMessageQueue
	stop        chan struct{}
	done        chan struct{}
	failures    chan error
	ready       chan struct{}
	closeOnce   sync.Once
	closed      atomic.Bool
	connected   atomic.Bool
	generation  atomic.Uint64

	connectionCount      atomic.Uint64
	reconnectCount       atomic.Uint64
	deliveryCount        atomic.Uint64
	deliveryFailureCount atomic.Uint64
	dropCount            atomic.Uint64
	diagnosticCount      atomic.Uint64
	diagnosticDropCount  atomic.Uint64
	lastConnectedNano    atomic.Int64
	lastDeliveredNano    atomic.Int64
	lastDiagnosticNano   atomic.Int64

	errorMu        sync.RWMutex
	transportError string
	diagnosticMu   sync.RWMutex
	diagnostics    []MQTTDiagnosticEvent
}

func (transport *mqttTransport) Publish(ctx context.Context, snapshot SourceSnapshot) error {
	if transport == nil || ctx == nil || transport.closed.Load() {
		return ErrMQTTTransportClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publishedAt := transport.now().UTC()
	payload, err := transport.compiled.RenderWithContext(JSONPayloadRenderContext{PublisherID: transport.publisherID, PublishedAt: publishedAt, Snapshot: snapshot})
	if err != nil {
		return err
	}
	if transport.queue.Push(mqttOutboundMessage{payload: append([]byte(nil), payload...)}) {
		transport.dropCount.Add(1)
	}
	return nil
}

func (transport *mqttTransport) Close(ctx context.Context) error {
	if transport == nil || ctx == nil {
		return ErrInvalidInput
	}
	transport.closeOnce.Do(func() {
		transport.closed.Store(true)
		close(transport.stop)
		if transport.client != nil {
			transport.client.Disconnect(250)
		}
	})
	select {
	case <-transport.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (transport *mqttTransport) Failures() <-chan error { return transport.failures }

func (transport *mqttTransport) Metrics() TransportMetrics {
	if transport == nil {
		return TransportMetrics{}
	}
	metrics := TransportMetrics{
		Connected: transport.connected.Load(), ConnectionCount: transport.connectionCount.Load(), ReconnectCount: transport.reconnectCount.Load(),
		DeliveryCount: transport.deliveryCount.Load(), DeliveryFailureCount: transport.deliveryFailureCount.Load(),
		TransportQueueDepth: uint64(transport.queue.Depth()), TransportDropCount: transport.dropCount.Load(),
		DiagnosticCount: transport.diagnosticCount.Load(), DiagnosticDropCount: transport.diagnosticDropCount.Load(), TransportError: transport.currentError(),
	}
	metrics.LastConnectedAt = atomicTime(&transport.lastConnectedNano)
	metrics.LastDeliveredAt = atomicTime(&transport.lastDeliveredNano)
	metrics.LastDiagnosticAt = atomicTime(&transport.lastDiagnosticNano)
	return metrics
}

func (transport *mqttTransport) Diagnostics() []MQTTDiagnosticEvent {
	if transport == nil {
		return []MQTTDiagnosticEvent{}
	}
	transport.diagnosticMu.RLock()
	defer transport.diagnosticMu.RUnlock()
	return append([]MQTTDiagnosticEvent(nil), transport.diagnostics...)
}

func (transport *mqttTransport) run() {
	defer close(transport.done)
	go transport.connectInitial()
	for {
		if !transport.connected.Load() {
			select {
			case <-transport.stop:
				return
			case <-transport.ready:
			}
			continue
		}
		message, exists := transport.queue.Pop()
		if !exists {
			select {
			case <-transport.stop:
				return
			case <-transport.ready:
			case <-transport.queue.Notify():
			}
			continue
		}
		token := transport.client.Publish(transport.config.Publish.Topic, transport.config.Publish.QoS, transport.config.Publish.Retain, message.payload)
		timer := time.NewTimer(time.Duration(transport.config.PublishTimeoutMS) * time.Millisecond)
		select {
		case <-transport.stop:
			timer.Stop()
			return
		case <-timer.C:
			transport.deliveryFailureCount.Add(1)
			transport.setError("MQTT publish acknowledgement timed out")
			if transport.queue.PushFront(message) {
				transport.dropCount.Add(1)
			}
			transport.afterPublishFailure()
		case <-token.Done():
			timer.Stop()
			if err := token.Error(); err != nil {
				transport.deliveryFailureCount.Add(1)
				transport.setError(err.Error())
				if transport.queue.PushFront(message) {
					transport.dropCount.Add(1)
				}
				transport.afterPublishFailure()
			} else {
				transport.deliveryCount.Add(1)
				transport.lastDeliveredNano.Store(transport.now().UTC().UnixNano())
				transport.clearError()
			}
		}
	}
}

func (transport *mqttTransport) connectInitial() {
	backoff := time.Duration(transport.config.ReconnectMinMS) * time.Millisecond
	maximum := time.Duration(transport.config.ReconnectMaxMS) * time.Millisecond
	for attempt := uint64(0); ; attempt++ {
		if attempt > 0 {
			transport.reconnectCount.Add(1)
		}
		token := transport.client.Connect()
		select {
		case <-transport.stop:
			return
		case <-token.Done():
			if token.Error() == nil {
				return
			}
			transport.setError(token.Error().Error())
		case <-time.After(time.Duration(transport.config.ConnectTimeoutMS) * time.Millisecond):
			transport.setError("MQTT connection timed out")
		}
		select {
		case <-transport.stop:
			return
		case <-time.After(jitterMQTTBackoff(backoff)):
		}
		backoff = min(backoff*2, maximum)
	}
}

func (transport *mqttTransport) afterPublishFailure() {
	if !transport.client.IsConnectionOpen() {
		transport.connected.Store(false)
		return
	}
	select {
	case <-transport.stop:
	case <-time.After(time.Duration(transport.config.ReconnectMinMS) * time.Millisecond):
	}
}

func (transport *mqttTransport) onConnected() {
	if transport.closed.Load() {
		return
	}
	generation := transport.generation.Add(1)
	transport.connectionCount.Add(1)
	transport.lastConnectedNano.Store(transport.now().UTC().UnixNano())
	transport.clearError()
	go transport.subscribeDiagnostics(generation)
}

func (transport *mqttTransport) subscribeDiagnostics(generation uint64) {
	if len(transport.config.Diagnostics) == 0 {
		transport.markReady(generation)
		return
	}
	filters := make(map[string]byte, len(transport.config.Diagnostics))
	for _, subscription := range transport.config.Diagnostics {
		filters[subscription.TopicFilter] = subscription.QoS
	}
	token := transport.client.Subscribe(filters, transport.onDiagnostic)
	select {
	case <-transport.stop:
		return
	case <-token.Done():
		if err := token.Error(); err != nil {
			transport.setError(err.Error())
			return
		}
		transport.markReady(generation)
	}
}

func (transport *mqttTransport) markReady(generation uint64) {
	if transport.closed.Load() || transport.generation.Load() != generation {
		return
	}
	transport.connected.Store(true)
	select {
	case transport.ready <- struct{}{}:
	default:
	}
}

func (transport *mqttTransport) onConnectionLost(err error) {
	transport.connected.Store(false)
	if err != nil {
		transport.setError(err.Error())
	}
}

func (transport *mqttTransport) onReconnecting() {
	transport.connected.Store(false)
	transport.reconnectCount.Add(1)
}

func (transport *mqttTransport) onDiagnostic(message mqttWireMessage) {
	if transport == nil || transport.closed.Load() {
		return
	}
	now := transport.now().UTC()
	sequence := transport.diagnosticCount.Add(1)
	payload := append([]byte(nil), message.Payload...)
	truncated := len(payload) > maxMQTTDiagnosticPayloadBytes
	if truncated {
		payload = payload[:maxMQTTDiagnosticPayloadBytes]
	}
	format := "binary_base64"
	presentation := base64.StdEncoding.EncodeToString(payload)
	if json.Valid(payload) {
		var compact bytes.Buffer
		_ = json.Compact(&compact, payload)
		format = "json"
		presentation = compact.String()
	} else if utf8.Valid(payload) {
		format = "text"
		presentation = string(payload)
	}
	event := MQTTDiagnosticEvent{
		Sequence: sequence, Label: transport.diagnosticLabel(message.Topic), Topic: message.Topic, QoS: message.QoS,
		Retained: message.Retained, Duplicate: message.Duplicate, ReceivedAt: now, Format: format, Payload: presentation, Truncated: truncated,
	}
	transport.diagnosticMu.Lock()
	if len(transport.diagnostics) == cap(transport.diagnostics) {
		copy(transport.diagnostics, transport.diagnostics[1:])
		transport.diagnostics[len(transport.diagnostics)-1] = event
		transport.diagnosticDropCount.Add(1)
	} else {
		transport.diagnostics = append(transport.diagnostics, event)
	}
	transport.diagnosticMu.Unlock()
	transport.lastDiagnosticNano.Store(now.UnixNano())
}

func (transport *mqttTransport) diagnosticLabel(topic string) string {
	for _, subscription := range transport.config.Diagnostics {
		if mqttTopicMatches(subscription.TopicFilter, topic) {
			return subscription.Label
		}
	}
	return "diagnostic"
}

func (transport *mqttTransport) setError(message string) {
	transport.errorMu.Lock()
	transport.transportError = sanitizeRuntimeErrorText(message)
	transport.errorMu.Unlock()
}

func (transport *mqttTransport) clearError() {
	transport.errorMu.Lock()
	transport.transportError = ""
	transport.errorMu.Unlock()
}

func (transport *mqttTransport) currentError() string {
	transport.errorMu.RLock()
	defer transport.errorMu.RUnlock()
	return transport.transportError
}

type mqttMessageQueue struct {
	mu       sync.Mutex
	capacity int
	items    []mqttOutboundMessage
	notify   chan struct{}
}

func newMQTTMessageQueue(capacity int) *mqttMessageQueue {
	return &mqttMessageQueue{capacity: capacity, items: make([]mqttOutboundMessage, 0, capacity), notify: make(chan struct{}, 1)}
}

func (queue *mqttMessageQueue) Push(message mqttOutboundMessage) bool {
	queue.mu.Lock()
	dropped := len(queue.items) == queue.capacity
	if dropped {
		copy(queue.items, queue.items[1:])
		queue.items[len(queue.items)-1] = message
	} else {
		queue.items = append(queue.items, message)
	}
	queue.mu.Unlock()
	queue.signal()
	return dropped
}

func (queue *mqttMessageQueue) PushFront(message mqttOutboundMessage) bool {
	queue.mu.Lock()
	dropped := len(queue.items) == queue.capacity
	if len(queue.items) == queue.capacity {
		queue.items = queue.items[:len(queue.items)-1]
	}
	queue.items = append(queue.items, mqttOutboundMessage{})
	copy(queue.items[1:], queue.items[:len(queue.items)-1])
	queue.items[0] = message
	queue.mu.Unlock()
	queue.signal()
	return dropped
}

func (queue *mqttMessageQueue) Pop() (mqttOutboundMessage, bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if len(queue.items) == 0 {
		return mqttOutboundMessage{}, false
	}
	message := queue.items[0]
	copy(queue.items, queue.items[1:])
	queue.items = queue.items[:len(queue.items)-1]
	return message, true
}

func (queue *mqttMessageQueue) Depth() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.items)
}

func (queue *mqttMessageQueue) Notify() <-chan struct{} { return queue.notify }

func (queue *mqttMessageQueue) signal() {
	select {
	case queue.notify <- struct{}{}:
	default:
	}
}

type pahoMQTTWireClient struct{ client mqtt.Client }

func newPahoMQTTWireClient(settings mqttWireSettings) (mqttWireClient, error) {
	options := mqtt.NewClientOptions().AddBroker(settings.BrokerURI).SetClientID(settings.ClientID).SetProtocolVersion(4)
	options.SetCleanSession(false).SetResumeSubs(true).SetOrderMatters(false).SetAutoReconnect(true)
	options.SetKeepAlive(settings.KeepAlive).SetConnectTimeout(settings.ConnectTimeout).SetWriteTimeout(settings.WriteTimeout)
	options.SetConnectRetry(false).SetConnectRetryInterval(settings.ReconnectMin).SetMaxReconnectInterval(settings.ReconnectMax)
	if settings.Username != "" {
		options.SetUsername(settings.Username)
		options.SetPassword(settings.Password)
	}
	if settings.TLS != nil {
		options.SetTLSConfig(settings.TLS)
	}
	options.SetOnConnectHandler(func(mqtt.Client) { settings.OnConnected() })
	options.SetConnectionLostHandler(func(_ mqtt.Client, err error) { settings.OnConnectionLost(err) })
	options.SetReconnectingHandler(func(mqtt.Client, *mqtt.ClientOptions) { settings.OnReconnecting() })
	options.SetDefaultPublishHandler(func(_ mqtt.Client, message mqtt.Message) { settings.DefaultMessage(pahoWireMessage(message)) })
	return &pahoMQTTWireClient{client: mqtt.NewClient(options)}, nil
}

func (client *pahoMQTTWireClient) Connect() mqttWireToken { return client.client.Connect() }
func (client *pahoMQTTWireClient) Publish(topic string, qos byte, retained bool, payload []byte) mqttWireToken {
	return client.client.Publish(topic, qos, retained, payload)
}
func (client *pahoMQTTWireClient) Subscribe(filters map[string]byte, handler func(mqttWireMessage)) mqttWireToken {
	return client.client.SubscribeMultiple(filters, func(_ mqtt.Client, message mqtt.Message) { handler(pahoWireMessage(message)) })
}
func (client *pahoMQTTWireClient) Disconnect(quiesce uint) {
	client.client.Disconnect(quiesce)
}
func (client *pahoMQTTWireClient) IsConnectionOpen() bool { return client.client.IsConnectionOpen() }

func pahoWireMessage(message mqtt.Message) mqttWireMessage {
	return mqttWireMessage{Topic: message.Topic(), QoS: message.Qos(), Retained: message.Retained(), Duplicate: message.Duplicate(), Payload: append([]byte(nil), message.Payload()...)}
}

func resolveMQTTOpaqueSecret(ctx context.Context, resolver SecretResolver, publisherID uuid.UUID, reference *SecretReference) ([]byte, error) {
	if reference == nil {
		return nil, nil
	}
	material, _, err := resolver.Resolve(ctx, publisherID, *reference, SecretKindOpaque)
	if err != nil {
		return nil, err
	}
	defer material.Destroy()
	if len(material.Opaque) == 0 {
		return nil, ErrInvalidSecretMaterial
	}
	return append([]byte(nil), material.Opaque...), nil
}

func mqttPahoBrokerURI(brokerURL string) string {
	if strings.HasPrefix(brokerURL, "mqtts://") {
		return "ssl://" + strings.TrimPrefix(brokerURL, "mqtts://")
	}
	return "tcp://" + strings.TrimPrefix(brokerURL, "mqtt://")
}

func mqttBrokerHost(brokerURL string) string {
	parsed, err := url.Parse(brokerURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func mqttTopicMatches(filter, topic string) bool {
	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	for index, level := range filterLevels {
		if level == "#" {
			return true
		}
		if index >= len(topicLevels) || level != "+" && level != topicLevels[index] {
			return false
		}
	}
	return len(filterLevels) == len(topicLevels)
}

func atomicTime(value *atomic.Int64) *time.Time {
	if unixNano := value.Load(); unixNano > 0 {
		at := time.Unix(0, unixNano).UTC()
		return &at
	}
	return nil
}

func jitterMQTTBackoff(duration time.Duration) time.Duration {
	spread := duration / 5
	if spread <= 0 {
		return duration
	}
	return duration - spread + time.Duration(rand.Int64N(int64(spread*2)+1))
}

func validateMQTTTransportPublisher(entity Publisher) (MQTTPublisherConfig, error) {
	if entity.ID == uuid.Nil || entity.Type != TypeMQTT || entity.ConfigVersion != 3 {
		return MQTTPublisherConfig{}, ErrInvalidMQTTPublisher
	}
	return ParseMQTTPublisherConfig(entity.Config)
}

var _ TransportFactory = (*MQTTTransportFactory)(nil)
var _ ResolvedTransportFactory = (*MQTTTransportFactory)(nil)
var _ Transport = (*mqttTransport)(nil)
var _ TransportMetricsProvider = (*mqttTransport)(nil)
var _ TransportFailureSource = (*mqttTransport)(nil)
