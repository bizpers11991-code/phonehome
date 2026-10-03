package alert

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// MQTT publishes to a broker with a minimal MQTT 3.1.1 client: CONNECT,
// PUBLISH at QoS 0 and DISCONNECT, one connection per check. It is both a
// Notifier (alerts go to <topic>/alert) and a Publisher (each device's grade
// goes to <topic>/device/<id>/grade, retained, with Home Assistant
// discovery so every device gets a grade sensor).
type MQTT struct {
	Broker             string // mqtt://host:1883 or mqtts://host:8883
	Username, Password string
	ClientID           string
	Topic              string // prefix, e.g. "phonehome"
	Discovery          bool   // publish Home Assistant discovery messages
	DiscoveryPrefix    string // e.g. "homeassistant"
	Version            string // phonehome's version, for the discovery origin
	TLS                *tls.Config
	Dial               func(ctx context.Context, network, addr string) (net.Conn, error) // nil: net.Dialer
}

func (m *MQTT) Name() string { return "mqtt" }

// Notify publishes the batch's JSON payload to <topic>/alert (not
// retained: an alert is an event, not a state).
func (m *MQTT) Notify(ctx context.Context, b Batch) error {
	return m.send(ctx, []message{{topic: m.Topic + "/alert", payload: JSON(b)}})
}

// Publish sends every device's current grade, retained, and its discovery
// config when Discovery is on.
func (m *MQTT) Publish(ctx context.Context, r model.HomeReport) error {
	var msgs []message
	for _, d := range r.Devices {
		if d.Grade == "" {
			continue
		}
		slug := topicSlug(d.Device.ID)
		state := fmt.Sprintf("%s/device/%s/grade", m.Topic, slug)
		if m.Discovery {
			cfg := discoveryConfig{
				Name:     "Privacy grade",
				UniqueID: "phonehome_" + slug + "_grade",
				State:    state,
				Icon:     "mdi:shield-search",
				Device: discoveryDevice{
					Identifiers:  []string{"phonehome_" + slug},
					Name:         device(d.Device).Name,
					Manufacturer: d.Device.Vendor,
				},
				Origin: discoveryOrigin{Name: "phonehome", Version: m.Version, URL: "https://github.com/bizpers11991-code/phonehome"},
			}
			payload, _ := json.Marshal(cfg)
			msgs = append(msgs, message{topic: fmt.Sprintf("%s/sensor/phonehome/%s/config", m.DiscoveryPrefix, cfg.UniqueID), payload: payload, retain: true})
		}
		msgs = append(msgs, message{topic: state, payload: []byte(d.Grade), retain: true})
	}
	if len(msgs) == 0 {
		return nil
	}
	return m.send(ctx, msgs)
}

// discoveryConfig is a Home Assistant MQTT discovery payload for one
// sensor ("single component discovery" in Home Assistant's MQTT docs).
type discoveryConfig struct {
	Name     string          `json:"name"`
	UniqueID string          `json:"unique_id"`
	State    string          `json:"state_topic"`
	Icon     string          `json:"icon"`
	Device   discoveryDevice `json:"device"`
	Origin   discoveryOrigin `json:"origin"`
}

type discoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer,omitempty"`
}

type discoveryOrigin struct {
	Name    string `json:"name"`
	Version string `json:"sw_version,omitempty"`
	URL     string `json:"support_url"`
}

// topicSlug turns a device id ("mac:aa:bb:…", "ip:192.168.1.5") into the
// characters Home Assistant allows in discovery ids: [a-zA-Z0-9_-].
func topicSlug(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		}
		return '_'
	}, id)
}

type message struct {
	topic   string
	payload []byte
	retain  bool
}

// MQTT control packet types (MQTT 3.1.1, section 2.2.1).
const (
	pktConnect    = 1
	pktConnack    = 2
	pktPublish    = 3
	pktDisconnect = 14
)

func (m *MQTT) send(ctx context.Context, msgs []message) error {
	u, err := url.Parse(m.Broker)
	if err != nil {
		return fmt.Errorf("mqtt: broker %q: %w", m.Broker, err)
	}
	secure := u.Scheme == "mqtts" || u.Scheme == "ssl" || u.Scheme == "tls"
	addr := u.Host
	if u.Port() == "" {
		port := "1883"
		if secure {
			port = "8883"
		}
		addr = net.JoinHostPort(u.Hostname(), port)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dial := m.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mqtt: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if secure {
		cfg := m.TLS
		if cfg == nil {
			cfg = &tls.Config{}
		}
		cfg = cfg.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = u.Hostname()
		}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("mqtt: %w", err)
		}
		conn = tc
	}

	w := bufio.NewWriter(conn)
	w.Write(connectPacket(m.ClientID, m.Username, m.Password))
	if err := w.Flush(); err != nil {
		return fmt.Errorf("mqtt: connect: %w", err)
	}
	if err := readConnack(conn); err != nil {
		return err
	}
	for _, msg := range msgs {
		p, err := publishPacket(msg)
		if err != nil {
			return err
		}
		w.Write(p)
	}
	w.Write([]byte{pktDisconnect << 4, 0})
	if err := w.Flush(); err != nil {
		return fmt.Errorf("mqtt: publish: %w", err)
	}
	return nil
}

// connectPacket builds CONNECT (3.1.1 section 3.1): protocol name "MQTT",
// level 4, a clean session, a 60-second keep-alive, then the client id and
// the optional user name and password.
func connectPacket(clientID, user, pass string) []byte {
	var body []byte
	body = appendString(body, "MQTT")
	flags := byte(0x02) // clean session
	if user != "" {
		flags |= 0x80
		if pass != "" {
			flags |= 0x40
		}
	}
	body = append(body, 4, flags, 0, 60)
	body = appendString(body, clientID)
	if user != "" {
		body = appendString(body, user)
		if pass != "" {
			body = appendString(body, pass)
		}
	}
	return packet(pktConnect<<4, body)
}

// publishPacket builds PUBLISH (section 3.3) at QoS 0, which has no packet
// identifier, optionally retained.
func publishPacket(m message) ([]byte, error) {
	if m.topic == "" || len(m.topic) > 0xffff || strings.ContainsAny(m.topic, "#+\x00") {
		return nil, fmt.Errorf("mqtt: invalid topic %q", m.topic)
	}
	head := byte(pktPublish << 4)
	if m.retain {
		head |= 0x01
	}
	return packet(head, append(appendString(nil, m.topic), m.payload...)), nil
}

// readConnack reads CONNACK (section 3.2) and turns a refusal into an
// error that says why.
func readConnack(r io.Reader) error {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return fmt.Errorf("mqtt: no CONNACK: %w", err)
	}
	if b[0] != pktConnack<<4 || b[1] != 2 {
		return fmt.Errorf("mqtt: expected CONNACK, got % x", b)
	}
	reasons := []string{"", "unacceptable protocol version", "client id rejected", "server unavailable", "bad user name or password", "not authorised"}
	switch rc := b[3]; {
	case rc == 0:
		return nil
	case int(rc) < len(reasons):
		return errors.New("mqtt: broker refused the connection: " + reasons[rc])
	default:
		return fmt.Errorf("mqtt: broker refused the connection (code %d)", rc)
	}
}

// packet prefixes body with the fixed header: the first byte and the
// remaining length as a variable-length integer (section 2.2.3).
func packet(first byte, body []byte) []byte {
	out := []byte{first}
	n := len(body)
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

// appendString appends a UTF-8 string with its two-byte length (1.5.3).
func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...)
}
