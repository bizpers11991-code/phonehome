package alert

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// maxLines caps the events listed in one text notification.
const maxLines = 20

// Text renders a batch for people: a title and one line per event.
func Text(b Batch) (title, body string) {
	demo := ""
	if b.Demo {
		demo = "[DEMO DATA] "
	}
	if len(b.Events) == 1 {
		title = demo + "phonehome: " + headline(b.Events[0])
	} else {
		title = fmt.Sprintf("%sphonehome: %d changes", demo, len(b.Events))
	}
	var lines []string
	for i, e := range b.Events {
		if i == maxLines {
			lines = append(lines, fmt.Sprintf("…and %d more; see the dashboard.", len(b.Events)-maxLines))
			break
		}
		lines = append(lines, sentence(e))
	}
	return title, strings.Join(lines, "\n")
}

func headline(e Event) string {
	switch e.Kind {
	case NewDevice:
		return "new device"
	case GradeWorse:
		return e.Device.Name + " is now graded " + e.Grade
	case Heartbeat:
		return e.Device.Name + " started a heartbeat"
	case Bypass:
		return e.Device.Name + " can bypass your DNS"
	}
	return e.Kind
}

func sentence(e Event) string {
	n := e.Device.Name
	switch e.Kind {
	case NewDevice:
		s := fmt.Sprintf("New device: %s (%s)", n, e.Device.Kind)
		if e.Grade != "" {
			s += ", grade " + e.Grade
		}
		return s + "."
	case GradeWorse:
		return fmt.Sprintf("%s got worse: grade %s → %s.", n, e.Before, e.Grade)
	case Heartbeat:
		return fmt.Sprintf("%s started contacting %s every %s (%s).", n, e.Domain, every(e.Every), e.Category.Label())
	case Bypass:
		return fmt.Sprintf("%s: %s", n, e.Finding.Detail)
	}
	return n + ": " + e.Kind
}

// every words an interval like the dashboard does: seconds below 90 s,
// minutes below 90 min, else hours.
func every(d time.Duration) string {
	switch s := d.Seconds(); {
	case s < 90:
		return fmt.Sprintf("%.0fs", s)
	case s < 5400:
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	return fmt.Sprintf("%.0fh", d.Hours())
}

// payload is the JSON a webhook or the MQTT alert topic receives. Every
// field is documented in docs/alerts.md; keep the two in step.
type payload struct {
	Source string        `json:"source"` // always "phonehome"
	Time   string        `json:"time"`
	Demo   bool          `json:"demo"`
	Title  string        `json:"title"`
	Text   string        `json:"text"`
	Events []eventRecord `json:"events"`
}

type eventRecord struct {
	Type          string         `json:"type"`
	Device        deviceRecord   `json:"device"`
	Grade         string         `json:"grade,omitempty"`
	PreviousGrade string         `json:"previousGrade,omitempty"`
	Domain        string         `json:"domain,omitempty"`
	Category      model.Category `json:"category,omitempty"`
	EverySeconds  float64        `json:"everySeconds,omitempty"`
	Finding       *findingRecord `json:"finding,omitempty"`
}

type deviceRecord struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Kind   model.DeviceKind `json:"kind"`
	Vendor string           `json:"vendor,omitempty"`
}

type findingRecord struct {
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	Evidence   string `json:"evidence"`
	Confidence string `json:"confidence"`
}

// JSON renders a batch as the documented payload.
func JSON(b Batch) []byte {
	title, text := Text(b)
	p := payload{Source: "phonehome", Time: b.Time.UTC().Format(time.RFC3339), Demo: b.Demo, Title: title, Text: text, Events: []eventRecord{}}
	for _, e := range b.Events {
		r := eventRecord{
			Type:   e.Kind,
			Device: deviceRecord{ID: e.Device.ID, Name: e.Device.Name, Kind: e.Device.Kind, Vendor: e.Device.Vendor},
			Grade:  e.Grade, PreviousGrade: e.Before,
		}
		switch e.Kind {
		case Heartbeat:
			r.Domain, r.Category, r.EverySeconds = e.Domain, e.Category, e.Every.Seconds()
		case Bypass:
			f := e.Finding
			r.Finding = &findingRecord{Kind: f.Kind, Detail: f.Detail, Evidence: f.Evidence, Confidence: f.Confidence}
		}
		p.Events = append(p.Events, r)
	}
	out, _ := json.Marshal(p)
	return out
}

// httpTarget is the part every HTTP notifier shares.
type httpTarget struct {
	name   string
	client *http.Client
}

func (t httpTarget) post(ctx context.Context, target string, body []byte, header http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	c := t.client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		// Leave the URL out: an ntfy topic name is as good as a password.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s: %w", t.name, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s answered %s", t.name, resp.Status)
	}
	return nil
}

// Webhook POSTs the JSON payload to a URL.
type Webhook struct {
	URL     string
	Headers map[string]string
	Client  *http.Client
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Notify(ctx context.Context, b Batch) error {
	h := http.Header{"Content-Type": {"application/json"}, "User-Agent": {"phonehome"}}
	for k, v := range w.Headers {
		h.Set(k, v)
	}
	return httpTarget{"webhook", w.Client}.post(ctx, w.URL, JSON(b), h)
}

// Ntfy publishes the text to an ntfy topic (POST to the topic URL with the
// Title, Priority and Tags headers ntfy documents).
type Ntfy struct {
	URL                string // the topic's full address
	Token              string // access token, sent as a Bearer token
	Username, Password string
	Priority           string
	Client             *http.Client
}

func (n *Ntfy) Name() string { return "ntfy" }

// ntfyMaxBody is ntfy's message limit; a longer body arrives as a file
// attachment instead of a notification.
const ntfyMaxBody = 4096

func (n *Ntfy) Notify(ctx context.Context, b Batch) error {
	title, body := Text(b)
	if len(body) > ntfyMaxBody {
		// Keep whole lines that fit, then say there is more.
		const more = "\n…see the dashboard."
		cut := strings.LastIndexByte(body[:ntfyMaxBody-len(more)], '\n')
		if cut < 0 {
			cut = ntfyMaxBody - len(more)
			for cut > 0 && !utf8.RuneStart(body[cut]) {
				cut--
			}
		}
		body = body[:cut] + more
	}
	// Device names may be non-ASCII; ntfy reads RFC 2047 encoded headers.
	h := http.Header{"Title": {mime.BEncoding.Encode("UTF-8", title)}, "Tags": {"phonehome"}, "User-Agent": {"phonehome"}}
	if n.Priority != "" {
		h.Set("Priority", n.Priority)
	}
	switch {
	case n.Token != "":
		h.Set("Authorization", "Bearer "+n.Token)
	case n.Username != "":
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(n.Username+":"+n.Password)))
	}
	return httpTarget{"ntfy", n.Client}.post(ctx, n.URL, []byte(body), h)
}

// Gotify posts a message with an application token (POST /message with
// the X-Gotify-Key header and a title, message, priority JSON body).
type Gotify struct {
	URL      string // the server's address
	Token    string
	Priority int
	Client   *http.Client
}

func (g *Gotify) Name() string { return "gotify" }

func (g *Gotify) Notify(ctx context.Context, b Batch) error {
	title, text := Text(b)
	msg := map[string]any{"title": title, "message": text}
	if g.Priority != 0 {
		msg["priority"] = g.Priority // otherwise the application's default applies
	}
	body, _ := json.Marshal(msg)
	h := http.Header{"Content-Type": {"application/json"}, "X-Gotify-Key": {g.Token}, "User-Agent": {"phonehome"}}
	return httpTarget{"gotify", g.Client}.post(ctx, strings.TrimRight(g.URL, "/")+"/message", body, h)
}
