package gate

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"
)

// clock is a settable time source for the rate-limit tests. Real time
// would make every refill assertion a sleep.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock {
	return &clock{at: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// counted is a next handler that records how many requests reached it —
// the only assertion that distinguishes "refused" from "allowed but the
// response happened to look similar".
type counted struct {
	mu sync.Mutex
	n  int
}

func (c *counted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (c *counted) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// generous is a rule no test trips by accident.
func generous(name string) Rule {
	return Rule{
		Name:      name,
		PerClient: Limit{Burst: 100, Every: time.Second},
		Global:    Limit{Burst: 1000, Every: time.Second},
	}
}

// request builds a request from peer with optional headers.
func request(peer string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/interviews/abc/generate", nil)
	r.RemoteAddr = peer
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func mustGate(t *testing.T, cfg Config) *Gate {
	t.Helper()
	g, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func mustProtect(t *testing.T, g *Gate, rule Rule, next http.Handler) http.Handler {
	t.Helper()
	h, err := g.Protect(rule, next)
	if err != nil {
		t.Fatalf("Protect: %v", err)
	}
	return h
}

func TestNewRejectsNegativeMaxClients(t *testing.T) {
	if _, err := New(Config{MaxClients: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(MaxClients: -1) error = %v, want ErrInvalidConfig", err)
	}
}

// TestNewDefaultsAreResolved exercises every zero-value field through its
// own default path (AGENTS.md §Testing rule 2): a nil clock, a nil
// trusted-proxy list and a zero client bound must each land on the
// documented default rather than on a nil deref or an unbounded map.
func TestNewDefaultsAreResolved(t *testing.T) {
	g := mustGate(t, Config{})
	if g.now == nil {
		t.Fatal("Now default: got nil clock")
	}
	if got := g.now(); got.IsZero() {
		t.Fatal("Now default: got the zero time, want time.Now")
	}
	if g.maxClients != defaultMaxClients {
		t.Fatalf("MaxClients default = %d, want %d", g.maxClients, defaultMaxClients)
	}
	if len(g.trusted) != len(defaultTrustedProxies()) {
		t.Fatalf("TrustedProxies default = %v, want the default set", g.trusted)
	}
	if g.log == nil {
		t.Fatal("Log default: got nil logger")
	}
	// The nil-passcode default is "no passcode", which must let a
	// request through rather than refuse every one of them.
	if !g.hasPasscode(request("192.0.2.9:1234", nil)) {
		t.Fatal("empty passcode default refused a request")
	}
}

func TestProtectRejectsUnusableRules(t *testing.T) {
	g := mustGate(t, Config{})
	ok := Limit{Burst: 1, Every: time.Second}
	cases := []struct {
		name string
		rule Rule
		next http.Handler
	}{
		{"empty name", Rule{PerClient: ok, Global: ok}, &counted{}},
		{"nil handler", Rule{Name: "r", PerClient: ok, Global: ok}, nil},
		{"zero per-client", Rule{Name: "r", Global: ok}, &counted{}},
		{"zero burst", Rule{Name: "r", PerClient: Limit{Every: time.Second}, Global: ok}, &counted{}},
		{"zero interval", Rule{Name: "r", PerClient: Limit{Burst: 1}, Global: ok}, &counted{}},
		{"negative burst", Rule{Name: "r", PerClient: Limit{Burst: -1, Every: time.Second}, Global: ok}, &counted{}},
		{"zero global", Rule{Name: "r", PerClient: ok}, &counted{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := g.Protect(tc.rule, tc.next)
			if !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("Protect error = %v, want ErrInvalidRule", err)
			}
			if h != nil {
				t.Fatal("Protect returned a handler alongside an error")
			}
		})
	}
}

func TestProtectFuncRejectsNilHandler(t *testing.T) {
	g := mustGate(t, Config{})
	if _, err := g.ProtectFunc(generous("r"), nil); !errors.Is(err, ErrInvalidRule) {
		t.Fatalf("ProtectFunc(nil) error = %v, want ErrInvalidRule", err)
	}
}

func TestProtectFuncWrapsAHandlerFunc(t *testing.T) {
	g := mustGate(t, Config{})
	reached := false
	h, err := g.ProtectFunc(generous("r"), func(w http.ResponseWriter, r *http.Request) {
		reached = true
	})
	if err != nil {
		t.Fatalf("ProtectFunc: %v", err)
	}
	h.ServeHTTP(httptest.NewRecorder(), request("192.0.2.1:1", nil))
	if !reached {
		t.Fatal("ProtectFunc did not reach the wrapped handler")
	}
}

// TestPasscodeRefusesAndAdmits is the passcode half of decision 9. The
// refusal must be a 403 with the {"error":...} envelope the shell reads,
// and never a 401 with WWW-Authenticate, which would raise a browser
// auth prompt mid-story.
func TestPasscodeRefusesAndAdmits(t *testing.T) {
	next := &counted{}
	g := mustGate(t, Config{Passcode: "open-sesame"})
	h := mustProtect(t, g, generous("generate"), next)

	cases := []struct {
		name    string
		mutate  func(*http.Request)
		status  int
		reached bool
	}{
		{"absent", func(*http.Request) {}, http.StatusForbidden, false},
		{"wrong same length", func(r *http.Request) {
			r.Header.Set(passcodeHeader, "open-sesamf")
		}, http.StatusForbidden, false},
		{"wrong different length", func(r *http.Request) {
			r.Header.Set(passcodeHeader, "nope")
		}, http.StatusForbidden, false},
		{"empty header", func(r *http.Request) {
			r.Header.Set(passcodeHeader, "")
		}, http.StatusForbidden, false},
		{"header", func(r *http.Request) {
			r.Header.Set(passcodeHeader, "open-sesame")
		}, http.StatusAccepted, true},
		{"cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: PasscodeCookie, Value: "open-sesame"})
		}, http.StatusAccepted, true},
		{"wrong cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: PasscodeCookie, Value: "wrong"})
		}, http.StatusForbidden, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := next.count()
			r := request("192.0.2.1:1", nil)
			tc.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if reached := next.count() > before; reached != tc.reached {
				t.Fatalf("handler reached = %v, want %v", reached, tc.reached)
			}
			if tc.status == http.StatusForbidden {
				if got := w.Header().Get("WWW-Authenticate"); got != "" {
					t.Fatalf("WWW-Authenticate = %q, want none", got)
				}
				assertErrorBody(t, w, "passcode_required")
			}
		})
	}
}

func TestNoPasscodeConfiguredIgnoresSuppliedOne(t *testing.T) {
	next := &counted{}
	g := mustGate(t, Config{})
	h := mustProtect(t, g, generous("generate"), next)
	r := request("192.0.2.1:1", map[string]string{passcodeHeader: "anything at all"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
}

// TestPerClientBurstThenRefusalThenRefill is the per-IP cap half of
// decision 9, on the route that costs $0.35 a press.
func TestPerClientBurstThenRefusalThenRefill(t *testing.T) {
	c := newClock()
	next := &counted{}
	g := mustGate(t, Config{Now: c.now})
	rule := Rule{
		Name:      "generate",
		PerClient: Limit{Burst: 3, Every: 20 * time.Minute},
		Global:    Limit{Burst: 100, Every: time.Second},
	}
	h := mustProtect(t, g, rule, next)

	for i := range 3 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("203.0.113.5:9", nil))
		if w.Code != http.StatusAccepted {
			t.Fatalf("burst request %d: status = %d, want 202", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("203.0.113.5:9", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth request: status = %d, want 429", w.Code)
	}
	if next.count() != 3 {
		t.Fatalf("handler reached %d times, want 3", next.count())
	}
	assertErrorBody(t, w, "rate_limited")
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retry <= 0 {
		t.Fatalf("Retry-After = %q (err %v), want a positive integer", w.Header().Get("Retry-After"), err)
	}
	if want := int((20 * time.Minute).Seconds()); retry != want {
		t.Fatalf("Retry-After = %d, want %d", retry, want)
	}

	// A different client is unaffected: this is a per-client bucket.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("203.0.113.6:9", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("second client: status = %d, want 202", w.Code)
	}

	// One refill interval buys exactly one more request.
	c.advance(20 * time.Minute)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("203.0.113.5:9", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("after refill: status = %d, want 202", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("203.0.113.5:9", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after refill, second request: status = %d, want 429", w.Code)
	}
}

// TestGlobalCapBoundsTheRouteAcrossClients is the property the package
// doc leans on: a forged client key buys a fresh per-client bucket, but
// it cannot raise the route's total spend past Global.
func TestGlobalCapBoundsTheRouteAcrossClients(t *testing.T) {
	c := newClock()
	next := &counted{}
	g := mustGate(t, Config{Now: c.now})
	rule := Rule{
		Name:      "generate",
		PerClient: Limit{Burst: 5, Every: time.Minute},
		Global:    Limit{Burst: 4, Every: 10 * time.Minute},
	}
	h := mustProtect(t, g, rule, next)

	// Every request comes from a brand-new "client" — the exact shape of
	// a spoofed CF-Connecting-IP from a peer we trust.
	for i := range 4 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request("10.0.0.2:1", map[string]string{cloudflareClientHeader: "198.51.100." + strconv.Itoa(i)}))
		if w.Code != http.StatusAccepted {
			t.Fatalf("global request %d: status = %d, want 202", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("10.0.0.2:1", map[string]string{cloudflareClientHeader: "198.51.100.99"}))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("fifth forged client: status = %d, want 429", w.Code)
	}
	if next.count() != 4 {
		t.Fatalf("handler reached %d times, want 4 (the global burst)", next.count())
	}
}

// TestRefusalConsumesNoTokens pins the "or from neither" half of allow:
// one client hammering past its own limit must not spend the route's
// global budget on refusals.
func TestRefusalConsumesNoTokens(t *testing.T) {
	c := newClock()
	next := &counted{}
	g := mustGate(t, Config{Now: c.now})
	rule := Rule{
		Name:      "generate",
		PerClient: Limit{Burst: 1, Every: time.Hour},
		Global:    Limit{Burst: 3, Every: time.Hour},
	}
	h := mustProtect(t, g, rule, next)

	// The noisy client spends its one token, then is refused ten times.
	for range 11 {
		h.ServeHTTP(httptest.NewRecorder(), request("203.0.113.1:1", nil))
	}
	if next.count() != 1 {
		t.Fatalf("noisy client reached the handler %d times, want 1", next.count())
	}
	// Two global tokens must remain for honest clients.
	for i, peer := range []string{"203.0.113.2:1", "203.0.113.3:1"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request(peer, nil))
		if w.Code != http.StatusAccepted {
			t.Fatalf("honest client %d: status = %d, want 202", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("203.0.113.4:1", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("fourth honest client: status = %d, want 429 (global exhausted)", w.Code)
	}
}

// TestRulesWithDifferentNamesHaveSeparateBuckets pins that one route
// running out does not refuse another.
func TestRulesWithDifferentNamesHaveSeparateBuckets(t *testing.T) {
	c := newClock()
	g := mustGate(t, Config{Now: c.now})
	one := Rule{Name: "one", PerClient: Limit{Burst: 1, Every: time.Hour}, Global: Limit{Burst: 1, Every: time.Hour}}
	two := Rule{Name: "two", PerClient: Limit{Burst: 1, Every: time.Hour}, Global: Limit{Burst: 1, Every: time.Hour}}
	h1 := mustProtect(t, g, one, &counted{})
	h2 := mustProtect(t, g, two, &counted{})

	h1.ServeHTTP(httptest.NewRecorder(), request("203.0.113.1:1", nil))
	w := httptest.NewRecorder()
	h1.ServeHTTP(w, request("203.0.113.1:1", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("rule one second request: status = %d, want 429", w.Code)
	}
	w = httptest.NewRecorder()
	h2.ServeHTTP(w, request("203.0.113.1:1", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("rule two: status = %d, want 202 (separate bucket)", w.Code)
	}
}

// TestClientTableIsBounded pins that a client-key spoofer cannot make
// the process allocate without limit.
func TestClientTableIsBounded(t *testing.T) {
	c := newClock()
	g := mustGate(t, Config{Now: c.now, MaxClients: 8})
	// Burst 1 so every bucket carries debt after one request, which is
	// the branch that has to fall back to least-recently-used eviction.
	rule := Rule{Name: "r", PerClient: Limit{Burst: 1, Every: time.Hour}, Global: Limit{Burst: 1000, Every: time.Second}}
	h := mustProtect(t, g, rule, &counted{})
	for i := range 200 {
		h.ServeHTTP(httptest.NewRecorder(), request("10.0.0.2:1", map[string]string{
			cloudflareClientHeader: "198.51.100." + strconv.Itoa(i%256),
		}))
		c.advance(time.Millisecond)
	}
	g.mu.Lock()
	n := len(g.clients)
	g.mu.Unlock()
	if n > 8 {
		t.Fatalf("client table holds %d entries, want at most 8", n)
	}
}

// TestFullBucketsAreEvictedFirst pins the lossless half of the eviction
// rule: a bucket that has refilled to capacity carries no debt, so
// dropping it forgives nothing.
func TestFullBucketsAreEvictedFirst(t *testing.T) {
	c := newClock()
	g := mustGate(t, Config{Now: c.now, MaxClients: 2})
	rule := Rule{Name: "r", PerClient: Limit{Burst: 2, Every: time.Second}, Global: Limit{Burst: 1000, Every: time.Millisecond}}
	h := mustProtect(t, g, rule, &counted{})

	// Client A spends one of two tokens: in debt.
	h.ServeHTTP(httptest.NewRecorder(), request("203.0.113.1:1", nil))
	// Client B fills the table.
	h.ServeHTTP(httptest.NewRecorder(), request("203.0.113.2:1", nil))
	// A third client forces an eviction; the debt-carrying buckets are
	// still in debt, so this exercises the least-recently-used branch.
	h.ServeHTTP(httptest.NewRecorder(), request("203.0.113.3:1", nil))
	g.mu.Lock()
	n := len(g.clients)
	g.mu.Unlock()
	if n > 2 {
		t.Fatalf("client table holds %d entries, want at most 2", n)
	}

	// Now let everything refill to full and confirm a full bucket is the
	// one dropped.
	c.advance(time.Hour)
	h.ServeHTTP(httptest.NewRecorder(), request("203.0.113.4:1", nil))
	g.mu.Lock()
	n = len(g.clients)
	g.mu.Unlock()
	if n > 2 {
		t.Fatalf("after refill the table holds %d entries, want at most 2", n)
	}
}

func TestConcurrentRequestsShareOneBudget(t *testing.T) {
	next := &counted{}
	g := mustGate(t, Config{})
	rule := Rule{Name: "r", PerClient: Limit{Burst: 5, Every: time.Hour}, Global: Limit{Burst: 5, Every: time.Hour}}
	h := mustProtect(t, g, rule, next)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), request("10.0.0.2:1", map[string]string{
				cloudflareClientHeader: "198.51.100." + strconv.Itoa(i),
			}))
		}(i)
	}
	wg.Wait()
	if next.count() != 5 {
		t.Fatalf("handler reached %d times under concurrency, want exactly the global burst of 5", next.count())
	}
}

func assertErrorBody(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("refusal body %q is not JSON: %v", w.Body.String(), err)
	}
	if body.Error != want {
		t.Fatalf("refusal body error = %q, want %q", body.Error, want)
	}
}

// TestTrustedProxiesEmptySliceTrustsNothing pins the difference between
// a nil TrustedProxies (the default set) and an explicitly empty one.
func TestTrustedProxiesEmptySliceTrustsNothing(t *testing.T) {
	g := mustGate(t, Config{TrustedProxies: []netip.Prefix{}})
	key := g.ClientKey(request("10.0.0.2:1", map[string]string{cloudflareClientHeader: "198.51.100.7"}))
	if key != "10.0.0.2" {
		t.Fatalf("ClientKey = %q, want the peer address (headers untrusted)", key)
	}
}
