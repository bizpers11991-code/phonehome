package alert

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

var testBatch = Batch{
	Time: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	Events: []Event{
		{Kind: NewDevice, Device: Device{ID: "mac:aa:bb:cc:00:11:22", Name: "Küche TV", Kind: model.KindTV, Vendor: "Samsung"}, Grade: "F"},
		{Kind: Heartbeat, Device: Device{ID: "ip:192.168.1.9", Name: "Plug", Kind: model.KindPlug}, Domain: "metrics.example", Category: model.CatTelemetry, Every: time.Minute},
		{Kind: Bypass, Device: Device{ID: "ip:192.168.1.9", Name: "Plug", Kind: model.KindPlug},
			Finding: model.Bypass{Kind: "doh-lookup", Detail: "Looked up dns.google.", Evidence: "dns.google", Confidence: "medium"}},
	},
}

type captured struct {
	method, path string
	header       http.Header
	body         []byte
}

func capture(t *testing.T, code int) (*httptest.Server, *captured) {
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method, c.path, c.header = r.Method, r.URL.Path, r.Header
		c.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestWebhook(t *testing.T) {
	srv, c := capture(t, http.StatusNoContent)
	w := &Webhook{URL: srv.URL + "/hook", Headers: map[string]string{"Authorization": "Bearer s3cret"}}
	if err := w.Notify(context.Background(), testBatch); err != nil {
		t.Fatal(err)
	}
	if c.method != http.MethodPost || c.path != "/hook" || c.header.Get("Authorization") != "Bearer s3cret" ||
		c.header.Get("Content-Type") != "application/json" {
		t.Fatalf("request %s %s %v", c.method, c.path, c.header)
	}
	var p map[string]any
	if err := json.Unmarshal(c.body, &p); err != nil {
		t.Fatal(err)
	}
	want := `{"source":"phonehome","time":"2026-10-02T12:00:00Z","demo":false,"title":"phonehome: 3 changes",` +
		`"text":"New device: Küche TV (tv), grade F.\nPlug started contacting metrics.example every 60s (Telemetry).\nPlug: Looked up dns.google.",` +
		`"events":[{"type":"new_device","device":{"id":"mac:aa:bb:cc:00:11:22","name":"Küche TV","kind":"tv","vendor":"Samsung"},"grade":"F"},` +
		`{"type":"heartbeat","device":{"id":"ip:192.168.1.9","name":"Plug","kind":"plug"},"domain":"metrics.example","category":"telemetry","everySeconds":60},` +
		`{"type":"bypass","device":{"id":"ip:192.168.1.9","name":"Plug","kind":"plug"},"finding":{"kind":"doh-lookup","detail":"Looked up dns.google.","evidence":"dns.google","confidence":"medium"}}]}`
	if string(c.body) != want {
		t.Fatalf("payload\n%s\nwant\n%s", c.body, want)
	}

	srv, _ = capture(t, http.StatusInternalServerError)
	if err := (&Webhook{URL: srv.URL}).Notify(context.Background(), testBatch); err == nil {
		t.Fatal("a 500 must be an error")
	}
}

func TestNtfy(t *testing.T) {
	srv, c := capture(t, http.StatusOK)
	n := &Ntfy{URL: srv.URL + "/phonehome-alerts", Token: "tk_x", Priority: "high"}
	b := Batch{Events: testBatch.Events[:1]}
	if err := n.Notify(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if c.path != "/phonehome-alerts" || c.header.Get("Authorization") != "Bearer tk_x" || c.header.Get("Priority") != "high" ||
		c.header.Get("Title") != "phonehome: new device" {
		t.Fatalf("headers %v", c.header)
	}
	if string(c.body) != "New device: Küche TV (tv), grade F." {
		t.Fatalf("body %q", c.body)
	}

	n = &Ntfy{URL: srv.URL + "/t", Username: "u", Password: "p"}
	if err := n.Notify(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if u, p, ok := (&http.Request{Header: c.header}).BasicAuth(); !ok || u != "u" || p != "p" {
		t.Fatalf("basic auth %v", c.header)
	}
	// A non-ASCII title is RFC 2047 encoded.
	b.Events[0].Kind = GradeWorse
	n.Notify(context.Background(), b)
	if got := c.header.Get("Title"); !strings.HasPrefix(got, "=?UTF-8?b?") {
		t.Fatalf("title %q", got)
	}
	b.Events[0].Kind = NewDevice
}

func TestNtfyLongBody(t *testing.T) {
	srv, c := capture(t, http.StatusOK)
	var evs []Event
	for range 20 {
		evs = append(evs, Event{Kind: Bypass, Device: Device{Name: "Plug"},
			Finding: model.Bypass{Detail: strings.Repeat("Looked up a very long encrypted-DNS name. ", 6)}})
	}
	if err := (&Ntfy{URL: srv.URL + "/t"}).Notify(context.Background(), Batch{Events: evs}); err != nil {
		t.Fatal(err)
	}
	if len(c.body) > ntfyMaxBody || !strings.HasSuffix(string(c.body), "\n…see the dashboard.") {
		t.Fatalf("%d bytes, ends %q", len(c.body), c.body[len(c.body)-30:])
	}
}

func TestGotify(t *testing.T) {
	srv, c := capture(t, http.StatusOK)
	g := &Gotify{URL: srv.URL + "/", Token: "AbCd", Priority: 5}
	if err := g.Notify(context.Background(), Batch{Events: testBatch.Events[1:2]}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(c.body, &body)
	if c.path != "/message" || c.header.Get("X-Gotify-Key") != "AbCd" || body["title"] != "phonehome: Plug started a heartbeat" ||
		body["message"] != "Plug started contacting metrics.example every 60s (Telemetry)." || body["priority"] != 5.0 {
		t.Fatalf("%s %v %s", c.path, c.header, c.body)
	}
	g.Priority = 0
	g.Notify(context.Background(), Batch{Events: testBatch.Events[1:2]})
	if strings.Contains(string(c.body), "priority") {
		t.Fatalf("priority sent without being configured: %s", c.body)
	}
}

// broker is a fake MQTT broker on one side of a pipe: it checks CONNECT,
// answers CONNACK with code, and records the PUBLISH packets until
// DISCONNECT.
type broker struct {
	code     byte
	connect  []byte
	messages []message
	done     chan error
}

func (b *broker) dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	b.done = make(chan error, 1)
	go func() { b.done <- b.serve(server) }()
	return client, nil
}

func readPacket(r io.Reader) (byte, []byte, error) {
	var h [1]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for {
		var d [1]byte
		if _, err := io.ReadFull(r, d[:]); err != nil {
			return 0, nil, err
		}
		n += int(d[0]&0x7f) * mult
		mult *= 128
		if d[0]&0x80 == 0 {
			break
		}
	}
	body := make([]byte, n)
	_, err := io.ReadFull(r, body)
	return h[0], body, err
}

func (b *broker) serve(c net.Conn) error {
	defer c.Close()
	head, body, err := readPacket(c)
	if err != nil {
		return err
	}
	if head != 0x10 {
		return io.ErrUnexpectedEOF
	}
	b.connect = body
	c.Write([]byte{0x20, 2, 0, b.code})
	if b.code != 0 {
		return nil
	}
	for {
		head, body, err := readPacket(c)
		if err != nil {
			return err
		}
		switch head >> 4 {
		case pktDisconnect:
			return nil
		case pktPublish:
			n := int(binary.BigEndian.Uint16(body))
			b.messages = append(b.messages, message{topic: string(body[2 : 2+n]), payload: body[2+n:], retain: head&1 == 1})
		}
	}
}

func TestMQTT(t *testing.T) {
	b := &broker{}
	m := &MQTT{Broker: "mqtt://broker.lan", Username: "ph", Password: "pw", ClientID: "phonehome", Topic: "phonehome",
		Discovery: true, DiscoveryPrefix: "homeassistant", Version: "v0.3.0", Dial: b.dial}
	if err := m.Notify(context.Background(), testBatch); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	// CONNECT: "MQTT", level 4, user+password+clean session, keep-alive 60,
	// then client id, user name and password.
	want := "\x00\x04MQTT\x04\xc2\x00\x3c\x00\x09phonehome\x00\x02ph\x00\x02pw"
	if string(b.connect) != want {
		t.Fatalf("CONNECT % x\nwant    % x", b.connect, want)
	}
	if len(b.messages) != 1 || b.messages[0].topic != "phonehome/alert" || b.messages[0].retain ||
		string(b.messages[0].payload) != string(JSON(testBatch)) {
		t.Fatalf("alert publish %+v", b.messages)
	}

	b.messages = nil
	rep := model.HomeReport{Devices: []model.DeviceReport{
		{Device: model.Device{ID: "mac:aa:bb:cc:00:11:22", Label: "Living room TV", Vendor: "Samsung"}, Grade: "F"},
		{Device: model.Device{ID: "ip:192.168.1.9"}}, // no grade: nothing to publish
	}}
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	if len(b.messages) != 2 {
		t.Fatalf("got %d messages", len(b.messages))
	}
	cfg, state := b.messages[0], b.messages[1]
	if cfg.topic != "homeassistant/sensor/phonehome/phonehome_mac_aa_bb_cc_00_11_22_grade/config" || !cfg.retain ||
		state.topic != "phonehome/device/mac_aa_bb_cc_00_11_22/grade" || string(state.payload) != "F" || !state.retain {
		t.Fatalf("%+v", b.messages)
	}
	wantCfg := `{"name":"Privacy grade","unique_id":"phonehome_mac_aa_bb_cc_00_11_22_grade","state_topic":"phonehome/device/mac_aa_bb_cc_00_11_22/grade",` +
		`"icon":"mdi:shield-search","device":{"identifiers":["phonehome_mac_aa_bb_cc_00_11_22"],"name":"Living room TV","manufacturer":"Samsung"},` +
		`"origin":{"name":"phonehome","sw_version":"v0.3.0","support_url":"https://github.com/bizpers11991-code/phonehome"}}`
	if string(cfg.payload) != wantCfg {
		t.Fatalf("discovery config\n%s\nwant\n%s", cfg.payload, wantCfg)
	}

	// Nothing changed: no connection at all.
	before := b.done
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if b.done != before {
		t.Fatal("an unchanged report connected to the broker")
	}

	// A new grade goes out alone; a device gone from the week is cleared.
	b.messages = nil
	rep.Devices = []model.DeviceReport{{Device: model.Device{ID: "mac:01"}, Grade: "B"}}
	m.Discovery = false
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	if len(b.messages) != 2 || b.messages[0].topic != "phonehome/device/mac_01/grade" || string(b.messages[0].payload) != "B" ||
		b.messages[1].topic != "phonehome/device/mac_aa_bb_cc_00_11_22/grade" || string(b.messages[1].payload) != "None" || !b.messages[1].retain {
		t.Fatalf("%+v", b.messages)
	}

	// Once an hour everything is sent again.
	b.messages = nil
	m.refreshed = m.refreshed.Add(-time.Hour)
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	if len(b.messages) != 1 || string(b.messages[0].payload) != "B" {
		t.Fatalf("refresh %+v", b.messages)
	}

	// A refusal says why.
	b.code = 5
	err := m.Notify(context.Background(), testBatch)
	<-b.done
	if err == nil || !strings.Contains(err.Error(), "not authorised") {
		t.Fatalf("refusal: %v", err)
	}
}

// TestMQTTRemembers: a device that leaves the report while phonehome is
// restarting still has its retained grade cleared.
func TestMQTTRemembers(t *testing.T) {
	b, st := &broker{}, &memStore{}
	newMQTT := func() *MQTT { return &MQTT{Broker: "mqtt://b.lan", Topic: "phonehome", Dial: b.dial, Store: st} }
	rep := model.HomeReport{Devices: []model.DeviceReport{{Device: model.Device{ID: "mac:01"}, Grade: "C"}}}
	if err := newMQTT().Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	<-b.done
	b.messages = nil
	rep.Devices = []model.DeviceReport{{Device: model.Device{ID: "mac:02"}, Grade: "A"}}
	if err := newMQTT().Publish(context.Background(), rep); err != nil { // after a restart
		t.Fatal(err)
	}
	<-b.done
	if len(b.messages) != 2 || b.messages[1].topic != "phonehome/device/mac_01/grade" || string(b.messages[1].payload) != "None" {
		t.Fatalf("%+v", b.messages)
	}
	if st.m[mqttStateKey] != `["mac_02"]` {
		t.Fatalf("remembered %s", st.m[mqttStateKey])
	}
}

// TestMQTTProvisional: a provisional grade is not published, and the grade
// published before it stays.
func TestMQTTProvisional(t *testing.T) {
	b := &broker{}
	m := &MQTT{Broker: "mqtt://b.lan", Topic: "phonehome", Dial: b.dial}
	rep := model.HomeReport{Devices: []model.DeviceReport{{Device: model.Device{ID: "mac:01"}, Grade: "B"}}}
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	<-b.done
	b.messages = nil
	provisional := model.GradeReason{Provisional: true, Data: time.Hour}
	rep.Devices = []model.DeviceReport{
		{Device: model.Device{ID: "mac:01"}, Grade: "D", Reason: provisional},
		{Device: model.Device{ID: "mac:02"}, Grade: "F", Reason: provisional},
	}
	before := b.done
	if err := m.Publish(context.Background(), rep); err != nil {
		t.Fatal(err)
	}
	if b.done != before {
		<-b.done
		t.Fatalf("provisional grades published: %+v", b.messages)
	}
}

func TestMQTTPackets(t *testing.T) {
	// Remaining lengths over 127 take more than one byte (2.2.3).
	p := packet(0x30, make([]byte, 321))
	if p[1] != 0xc1 || p[2] != 0x02 || len(p) != 3+321 {
		t.Fatalf("% x", p[:3])
	}
	if _, err := publishPacket(message{topic: "a/#"}); err == nil {
		t.Fatal("wildcard topic accepted")
	}
	if got := connectPacket("c", "", ""); string(got) != "\x10\x0d\x00\x04MQTT\x04\x02\x00\x3c\x00\x01c" {
		t.Fatalf("% x", got)
	}
}
