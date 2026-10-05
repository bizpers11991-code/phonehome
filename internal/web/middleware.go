package web

import (
	"cmp"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const csp = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// localSuffixes are the domains a home network's own names live under.
// Nobody outside the LAN can make a browser resolve one of them.
var localSuffixes = []string{".local", ".lan", ".home.arpa", ".internal"}

// hostAllowed reports whether a request's Host header names this machine
// the way people on a LAN do: an IP address, localhost, pi.hole, a
// single-label name (pi, nas), a name under a local suffix, or one listed in allowed_hosts
// ("*.example.com" matches any name below example.com). A page on a public
// name re-pointed at this machine's address (DNS rebinding) still carries
// that public name, so it is refused.
func hostAllowed(host string, allowed []string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	// pi.hole is the name Pi-hole answers for itself, which phonehome often
	// shares a machine with.
	if host == "localhost" || host == "pi.hole" || !strings.Contains(host, ".") {
		return true
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	for _, a := range allowed {
		a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".")
		if suffix, ok := strings.CutPrefix(a, "*"); ok && strings.HasSuffix(host, suffix) || host == a {
			return true
		}
	}
	return false
}

// checkHost refuses requests addressed to a host name this server does not
// answer to, except /healthz, which reveals nothing.
func checkHost(next http.Handler, allowed []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && !hostAllowed(r.Host, allowed) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusMisdirectedRequest)
			w.Write([]byte("phonehome does not answer to this host name; add it to allowed_hosts in phonehome.yaml\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authDelay is how long a client address must wait after a wrong password
// before it may try again; attempts in between get 429 without a check.
// That keeps guessing to about one password a second per address.
const authDelay = time.Second

// failures remembers when each client address may next try a password.
// It is bounded: past maxFailures addresses, expired entries are dropped,
// and if none have expired the new one is not recorded.
type failures struct {
	mu   sync.Mutex
	next map[string]time.Time
	now  func() time.Time
}

const maxFailures = 4096

func (f *failures) blocked(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now().Before(f.next[ip])
}

func (f *failures) failed(ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if len(f.next) >= maxFailures {
		for k, t := range f.next {
			if !now.Before(t) {
				delete(f.next, k)
			}
		}
		if len(f.next) >= maxFailures {
			return
		}
	}
	f.next[ip] = now.Add(authDelay)
}

func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// basicAuth guards everything except /healthz. Credentials are hashed before
// comparison so neither their content nor their length leaks through timing.
func basicAuth(next http.Handler, user, pass string, now func() time.Time) http.Handler {
	wantUser, wantPass := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(pass))
	fails := &failures{next: map[string]time.Time{}, now: now}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if ok && fails.blocked(clientIP(r)) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "too many wrong passwords; wait a second and try again")
			return
		}
		gotUser, gotPass := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
		userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
		passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
		if !ok || userOK&passOK != 1 {
			if ok {
				fails.failed(clientIP(r))
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="phonehome", charset="UTF-8"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status, level := cmp.Or(rec.status, http.StatusOK), slog.LevelInfo
		if status >= 500 {
			level = slog.LevelWarn
		}
		s.log.LogAttrs(r.Context(), level, "http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("took", time.Since(start)),
		)
	})
}
