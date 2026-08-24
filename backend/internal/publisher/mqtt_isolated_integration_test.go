package publisher

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/google/uuid"
)

func TestMQTTIsolatedBrokerPlainTLSAndMTLS_Integration(t *testing.T) {
	pki := newIsolatedMQTTPKI(t)
	for _, test := range []struct {
		name           string
		tlsConfig      *tls.Config
		clientTLS      ClientTLSConfig
		materials      map[string]secretTestResolved
		wantClientName string
	}{
		{name: "plain MQTT without authentication"},
		{
			name: "TLS with custom CA", tlsConfig: pki.serverTLSConfig(false),
			clientTLS: ClientTLSConfig{ServerName: isolatedMQTTServerName, CustomCA: &SecretReference{Name: "tls.ca"}},
			materials: map[string]secretTestResolved{"tls.ca": {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: pki.caPEM}}},
		},
		{
			name: "mTLS with verified client identity", tlsConfig: pki.serverTLSConfig(true),
			clientTLS: ClientTLSConfig{
				ServerName: isolatedMQTTServerName, CustomCA: &SecretReference{Name: "tls.ca"}, ClientIdentity: &SecretReference{Name: "tls.client"},
			},
			materials: map[string]secretTestResolved{
				"tls.ca":     {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: pki.caPEM}},
				"tls.client": {kind: SecretKindClientIdentity, material: SecretMaterial{CertificatePEM: pki.clientCertificatePEM, PrivateKeyPEM: pki.clientKeyPEM}},
			},
			wantClientName: isolatedMQTTClientName,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker := newIsolatedMQTTBroker(t, test.tlsConfig)
			publisherID := uuid.New()
			resolver := &secretTestResolver{publisherID: publisherID, materials: test.materials}
			entity, sources, reference := isolatedMQTTPublisher(t, publisherID, broker.URL(test.tlsConfig != nil), test.clientTLS, true)
			factory, err := NewMQTTTransportFactory(resolver, NewJSONPayloadEngine())
			if err != nil {
				t.Fatalf("NewMQTTTransportFactory() error = %v", err)
			}
			created, err := factory.NewResolvedTransport(t.Context(), entity, sources)
			if err != nil {
				t.Fatalf("NewResolvedTransport() error = %v", err)
			}
			transport := created.(*mqttTransport)
			defer closeIsolatedMQTTTransport(t, transport)
			waitMQTTCondition(t, func() bool { return transport.Metrics().Connected })

			connection := receiveIsolatedMQTTConnect(t, broker.connects)
			if connection.ClientID != "isolated-client" || connection.Username || connection.Password {
				t.Fatalf("MQTT CONNECT = %#v; optional authentication must remain absent", connection)
			}
			observedAt := time.Date(2026, time.August, 23, 8, 35, 25, 0, time.UTC)
			if err := transport.Publish(t.Context(), SourceSnapshot{
				CapturedAt: observedAt,
				Samples: []SourceSample{{
					Alias: "value", Reference: reference, Available: true, SchemaVersion: 1, DataType: SourceDataTypeFloat64,
					Value: 42.5, Quality: SourceQualityGood, ObservedAt: &observedAt,
				}},
			}); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			published := receiveIsolatedMQTTPublish(t, broker.publishes)
			if published.Topic != isolatedMQTTTelemetryTopic || published.QoS != 1 || published.Retained || string(published.Payload) != `{"value":42.5}` {
				t.Fatalf("broker publish = %#v", published)
			}
			waitMQTTCondition(t, func() bool {
				metrics := transport.Metrics()
				return metrics.DeliveryCount == 1 && metrics.DiagnosticCount == 1
			})
			events := transport.Diagnostics()
			if len(events) != 1 || events[0].Label != "ack" || events[0].Topic != isolatedMQTTAckTopic || events[0].Format != "json" || events[0].Payload != `{"status":"success","processed":1}` {
				t.Fatalf("diagnostics = %#v", events)
			}
			if test.wantClientName != "" {
				select {
				case name := <-broker.clientNames:
					if name != test.wantClientName {
						t.Fatalf("mTLS client certificate = %q, want %q", name, test.wantClientName)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("broker did not observe the mTLS client certificate")
				}
			}
		})
	}
}

func TestMQTTIsolatedBrokerRejectsInvalidCertificatePaths_Integration(t *testing.T) {
	pki := newIsolatedMQTTPKI(t)
	for _, test := range []struct {
		name      string
		brokerTLS *tls.Config
		clientTLS ClientTLSConfig
		materials map[string]secretTestResolved
	}{
		{
			name: "private CA without custom trust", brokerTLS: pki.serverTLSConfig(false),
			clientTLS: ClientTLSConfig{ServerName: isolatedMQTTServerName},
		},
		{
			name: "verified CA with wrong server name", brokerTLS: pki.serverTLSConfig(false),
			clientTLS: ClientTLSConfig{ServerName: "wrong-broker.test", CustomCA: &SecretReference{Name: "tls.ca"}},
			materials: map[string]secretTestResolved{"tls.ca": {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: pki.caPEM}}},
		},
		{
			name: "mTLS broker without client identity", brokerTLS: pki.serverTLSConfig(true),
			clientTLS: ClientTLSConfig{ServerName: isolatedMQTTServerName, CustomCA: &SecretReference{Name: "tls.ca"}},
			materials: map[string]secretTestResolved{"tls.ca": {kind: SecretKindCACertificate, material: SecretMaterial{CertificatePEM: pki.caPEM}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker := newIsolatedMQTTBroker(t, test.brokerTLS)
			publisherID := uuid.New()
			resolver := &secretTestResolver{publisherID: publisherID, materials: test.materials}
			entity, sources, _ := isolatedMQTTPublisher(t, publisherID, broker.URL(true), test.clientTLS, false)
			tester, err := NewMQTTConnectionTester(resolver, NewJSONPayloadEngine())
			if err != nil {
				t.Fatalf("NewMQTTConnectionTester() error = %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := tester.Test(ctx, entity, sources, nil)
			if !errors.Is(err, ErrMQTTConnectionTLSFailed) || result.Connected {
				t.Fatalf("MQTTConnectionTester.Test() = %#v, %v; want TLS failure", result, err)
			}
		})
	}
}

const (
	isolatedMQTTServerName     = "broker.isolated.test"
	isolatedMQTTClientName     = "iot-edge-isolated-client"
	isolatedMQTTTelemetryTopic = "isolated/telemetry"
	isolatedMQTTAckTopic       = "isolated/ack"
)

type isolatedMQTTConnect struct {
	ClientID string
	Username bool
	Password bool
}

type isolatedMQTTPublish struct {
	Topic    string
	QoS      byte
	Retained bool
	Payload  []byte
}

type isolatedMQTTBroker struct {
	listener    net.Listener
	connects    chan isolatedMQTTConnect
	publishes   chan isolatedMQTTPublish
	clientNames chan string
	connections sync.Map
	wait        sync.WaitGroup
	closeOnce   sync.Once
	messageID   atomic.Uint32
}

func newIsolatedMQTTBroker(t *testing.T, tlsConfig *tls.Config) *isolatedMQTTBroker {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for isolated MQTT broker: %v", err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	broker := &isolatedMQTTBroker{
		listener: listener, connects: make(chan isolatedMQTTConnect, 8), publishes: make(chan isolatedMQTTPublish, 8), clientNames: make(chan string, 8),
	}
	broker.messageID.Store(700)
	broker.wait.Add(1)
	go broker.accept()
	t.Cleanup(broker.Close)
	return broker
}

func (broker *isolatedMQTTBroker) URL(secure bool) string {
	scheme := "mqtt"
	if secure {
		scheme = "mqtts"
	}
	return scheme + "://" + broker.listener.Addr().String()
}

func (broker *isolatedMQTTBroker) Close() {
	broker.closeOnce.Do(func() {
		_ = broker.listener.Close()
		broker.connections.Range(func(connection, _ any) bool {
			_ = connection.(net.Conn).Close()
			return true
		})
		broker.wait.Wait()
	})
}

func (broker *isolatedMQTTBroker) accept() {
	defer broker.wait.Done()
	for {
		connection, err := broker.listener.Accept()
		if err != nil {
			return
		}
		broker.connections.Store(connection, struct{}{})
		broker.wait.Add(1)
		go broker.serve(connection)
	}
}

func (broker *isolatedMQTTBroker) serve(connection net.Conn) {
	defer broker.wait.Done()
	defer broker.connections.Delete(connection)
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if tlsConnection, ok := connection.(*tls.Conn); ok {
		if err := tlsConnection.Handshake(); err != nil {
			return
		}
		state := tlsConnection.ConnectionState()
		if len(state.PeerCertificates) > 0 {
			broker.clientNames <- state.PeerCertificates[0].Subject.CommonName
		}
	}
	packet, err := packets.ReadPacket(connection)
	if err != nil {
		return
	}
	connect, ok := packet.(*packets.ConnectPacket)
	if !ok || connect.ProtocolVersion != 4 {
		return
	}
	broker.connects <- isolatedMQTTConnect{ClientID: connect.ClientIdentifier, Username: connect.UsernameFlag, Password: connect.PasswordFlag}
	connack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
	connack.ReturnCode = packets.Accepted
	if err := connack.Write(connection); err != nil {
		return
	}
	subscriptions := make(map[string]byte)
	for {
		packet, err := packets.ReadPacket(connection)
		if err != nil {
			return
		}
		switch packet := packet.(type) {
		case *packets.SubscribePacket:
			for index, topic := range packet.Topics {
				subscriptions[topic] = packet.Qoss[index]
			}
			suback := packets.NewControlPacket(packets.Suback).(*packets.SubackPacket)
			suback.MessageID = packet.MessageID
			suback.ReturnCodes = append([]byte(nil), packet.Qoss...)
			if err := suback.Write(connection); err != nil {
				return
			}
		case *packets.PublishPacket:
			broker.publishes <- isolatedMQTTPublish{Topic: packet.TopicName, QoS: packet.Qos, Retained: packet.Retain, Payload: append([]byte(nil), packet.Payload...)}
			if packet.Qos == 1 {
				puback := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
				puback.MessageID = packet.MessageID
				if err := puback.Write(connection); err != nil {
					return
				}
			}
			if packet.TopicName == isolatedMQTTTelemetryTopic {
				if qos, subscribed := subscriptions[isolatedMQTTAckTopic]; subscribed {
					diagnostic := packets.NewControlPacket(packets.Publish).(*packets.PublishPacket)
					diagnostic.TopicName = isolatedMQTTAckTopic
					diagnostic.Qos = qos
					diagnostic.Payload = []byte(`{"status":"success","processed":1}`)
					if qos > 0 {
						diagnostic.MessageID = uint16(broker.messageID.Add(1))
					}
					if err := diagnostic.Write(connection); err != nil {
						return
					}
				}
			}
		case *packets.PingreqPacket:
			if err := packets.NewControlPacket(packets.Pingresp).Write(connection); err != nil {
				return
			}
		case *packets.PubackPacket:
		case *packets.DisconnectPacket:
			return
		default:
			return
		}
	}
}

func isolatedMQTTPublisher(t *testing.T, publisherID uuid.UUID, brokerURL string, clientTLS ClientTLSConfig, enabled bool) (Publisher, []ResolvedSource, SourceReference) {
	t.Helper()
	reference := TagSource(uuid.New())
	sources := []ResolvedSource{{
		Alias: "value", Descriptor: SourceDescriptor{Reference: reference, SchemaVersion: 1, DataType: SourceDataTypeFloat64, PeriodKind: SourcePeriodInstantaneous},
	}}
	config := defaultMQTTPublisherConfig()
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 1000}
	config.MQTT.BrokerURL = brokerURL
	config.MQTT.PlaintextAcknowledged = clientTLS == (ClientTLSConfig{}) && strings.HasPrefix(brokerURL, "mqtt://")
	config.MQTT.ClientID = "isolated-client"
	config.MQTT.Auth = MQTTAuthConfig{}
	config.MQTT.TLS = clientTLS
	config.MQTT.Publish = MQTTPublishConfig{Topic: isolatedMQTTTelemetryTopic, QoS: 1, PayloadTemplate: `{"value":{{value "value"}}}`}
	config.MQTT.Diagnostics = []MQTTDiagnosticSubscription{{Label: "ack", TopicFilter: isolatedMQTTAckTopic, QoS: 1}}
	config.MQTT.KeepAliveMS = 10000
	config.MQTT.ConnectTimeoutMS = 1000
	config.MQTT.PublishTimeoutMS = 1000
	config.MQTT.ReconnectMinMS = 100
	config.MQTT.ReconnectMaxMS = 200
	config.MQTT.QueueCapacity = 8
	config.MQTT.DiagnosticHistoryDepth = 8
	normalized, err := normalizeMQTTPublisherConfig(mustJSON(t, config))
	if err != nil {
		t.Fatalf("normalizeMQTTPublisherConfig() error = %v", err)
	}
	return Publisher{ID: publisherID, Type: TypeMQTT, Name: "Isolated MQTT", Enabled: enabled, Config: normalized, ConfigVersion: 3}, sources, reference
}

func receiveIsolatedMQTTConnect(t *testing.T, connects <-chan isolatedMQTTConnect) isolatedMQTTConnect {
	t.Helper()
	select {
	case connection := <-connects:
		return connection
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for isolated MQTT CONNECT")
		return isolatedMQTTConnect{}
	}
}

func receiveIsolatedMQTTPublish(t *testing.T, publishes <-chan isolatedMQTTPublish) isolatedMQTTPublish {
	t.Helper()
	select {
	case published := <-publishes:
		return published
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for isolated MQTT PUBLISH")
		return isolatedMQTTPublish{}
	}
}

func closeIsolatedMQTTTransport(t *testing.T, transport Transport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transport.Close(ctx); err != nil {
		t.Errorf("MQTT transport Close() error = %v", err)
	}
}

type isolatedMQTTPKI struct {
	caCertificate        *x509.Certificate
	caKey                *ecdsa.PrivateKey
	caPEM                []byte
	serverCertificate    tls.Certificate
	clientCertificatePEM []byte
	clientKeyPEM         []byte
}

func newIsolatedMQTTPKI(t *testing.T) isolatedMQTTPKI {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating isolated MQTT CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Isolated MQTT Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("creating isolated MQTT CA: %v", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parsing isolated MQTT CA: %v", err)
	}
	pki := isolatedMQTTPKI{
		caCertificate: caCertificate, caKey: caKey,
		caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}
	serverCertificatePEM, serverKeyPEM := pki.issue(t, 2, isolatedMQTTServerName, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{isolatedMQTTServerName})
	pki.serverCertificate, err = tls.X509KeyPair(serverCertificatePEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("loading isolated MQTT server identity: %v", err)
	}
	pki.clientCertificatePEM, pki.clientKeyPEM = pki.issue(t, 3, isolatedMQTTClientName, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, nil)
	return pki
}

func (pki isolatedMQTTPKI) issue(t *testing.T, serial int64, commonName string, usage []x509.ExtKeyUsage, dnsNames []string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating %s key: %v", commonName, err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: commonName}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage, DNSNames: dnsNames,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, pki.caCertificate, &key.PublicKey, pki.caKey)
	if err != nil {
		t.Fatalf("creating %s certificate: %v", commonName, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling %s key: %v", commonName, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func (pki isolatedMQTTPKI) serverTLSConfig(requireClientCertificate bool) *tls.Config {
	config := &tls.Config{Certificates: []tls.Certificate{pki.serverCertificate}, MinVersion: tls.VersionTLS12}
	if requireClientCertificate {
		roots := x509.NewCertPool()
		roots.AddCert(pki.caCertificate)
		config.ClientAuth = tls.RequireAndVerifyClientCert
		config.ClientCAs = roots
	}
	return config
}
