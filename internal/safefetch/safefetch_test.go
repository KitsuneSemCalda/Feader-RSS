package safefetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIsGlobalIP(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"127.0.0.1", false},
		{"10.0.0.5", false},
		{"192.168.1.1", false},
		{"172.16.0.1", false},
		{"169.254.1.1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"::1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"fc00::1", false},
		{"fe80::1", false},
		{"2001:4860:4860::8888", true},
	}
	for _, c := range cases {
		addr := netip.MustParseAddr(c.addr)
		if got := isGlobalIP(addr); got != c.want {
			t.Errorf("isGlobalIP(%s) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestGetRejectsUnsupportedScheme(t *testing.T) {
	_, err := Get("ftp://example.com/file", time.Second)
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
}

func TestGetRejectsLoopback(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		// Some sandboxed CI runners disallow local sockets altogether. The
		// check remains active on normal developer/CI hosts where a listener
		// can be created.
		t.Skipf("loopback sockets unavailable: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	_, err = Get(srv.URL, time.Second)
	if err == nil {
		t.Fatal("expected error fetching loopback address")
	}
}

func TestGetRejectsMissingHost(t *testing.T) {
	_, err := Get("http:///no-host", time.Second)
	if err == nil {
		t.Fatal("expected error for missing hostname")
	}
}

func TestCheckResponseRejectsHTTPError(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
	err := checkResponse(resp)
	if err == nil {
		t.Fatal("expected non-2xx response to be rejected")
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusNotFound {
		t.Fatalf("error = %T (%v), want HTTPStatusError 404", err, err)
	}
}

func TestCheckResponseAllowsSuccess(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}
	if err := checkResponse(resp); err != nil {
		t.Fatalf("checkResponse: %v", err)
	}
}

func TestIsRetryableDistinguishesTransientAndPermanentFailures(t *testing.T) {
	if !IsRetryable(&HTTPStatusError{Code: http.StatusBadGateway, Status: "502 Bad Gateway"}) {
		t.Error("expected 502 to be retryable")
	}
	if !IsRetryable(&HTTPStatusError{Code: http.StatusTooManyRequests, Status: "429 Too Many Requests"}) {
		t.Error("expected 429 to be retryable")
	}
	if IsRetryable(&HTTPStatusError{Code: http.StatusNotFound, Status: "404 Not Found"}) {
		t.Error("did not expect 404 to be retryable")
	}
	if IsRetryable(errors.New("invalid RSS: malformed XML")) {
		t.Error("did not expect malformed feed to be retryable")
	}
}

func TestReadCappedRejectsOversized(t *testing.T) {
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'a'
	}
	r := newRepeatReader(big)
	_, err := ReadCapped(r, 50)
	if err == nil {
		t.Fatal("expected ErrResponseTooLarge")
	}
	if _, ok := err.(*ErrResponseTooLarge); !ok {
		t.Errorf("error type = %T, want *ErrResponseTooLarge", err)
	}
}

func TestReadCappedAllowsWithinLimit(t *testing.T) {
	data := []byte("hello world")
	r := newRepeatReader(data)
	got, err := ReadCapped(r, 50)
	if err != nil {
		t.Fatalf("ReadCapped: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestErrorMessagesAndRetryClassifications(t *testing.T) {
	if got := (&ErrResponseTooLarge{Limit: 42}).Error(); got != "response exceeds 42 byte limit" {
		t.Errorf("ErrResponseTooLarge.Error() = %q", got)
	}
	statusErr := &HTTPStatusError{Code: http.StatusBadGateway, Status: "502 Bad Gateway"}
	if got := statusErr.Error(); got != "unexpected HTTP status: 502 Bad Gateway" {
		t.Errorf("HTTPStatusError.Error() = %q", got)
	}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"too large", &ErrResponseTooLarge{Limit: 1}, false},
		{"server error", &HTTPStatusError{Code: http.StatusInternalServerError, Status: "500 Internal Server Error"}, true},
		{"rate limit", &HTTPStatusError{Code: http.StatusTooManyRequests, Status: "429 Too Many Requests"}, true},
		{"client error", &HTTPStatusError{Code: http.StatusBadRequest, Status: "400 Bad Request"}, false},
		{"invalid URL", errors.New("invalid URL: bad escape"), false},
		{"unsupported scheme", errors.New("unsupported URL scheme: ftp"), false},
		{"missing hostname", errors.New("URL is missing a hostname"), false},
		{"unsafe destination", errors.New("refusing to contact non-public address: 127.0.0.1"), false},
		{"redirect loop", errors.New("too many redirects"), false},
		{"invalid XML", errors.New("invalid XML: EOF"), false},
		{"invalid RSS", errors.New("invalid RSS: malformed"), false},
		{"invalid Atom", errors.New("invalid Atom: malformed"), false},
		{"unsupported feed", errors.New("unsupported feed format: root element <html>"), false},
		{"transport", errors.New("dial tcp: connection reset by peer"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetryable(tc.err); got != tc.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestReservedSpecialAddresses(t *testing.T) {
	cases := []string{
		"100.64.0.1",
		"192.0.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"240.0.0.1",
		"100::1",
		"2001:db8::1",
		"2002:c000:0204::1",
	}
	for _, value := range cases {
		addr := netip.MustParseAddr(value)
		if isGlobalIP(addr) {
			t.Errorf("isGlobalIP(%s) = true, want reserved/private", value)
		}
	}
}

func TestRequireSafeURLUsesResolverAndHandlesResolutionFailures(t *testing.T) {
	oldLookup := lookupNetIP
	t.Cleanup(func() { lookupNetIP = oldLookup })

	lookupNetIP = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		switch host {
		case "public.test":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}, nil
		case "empty.test":
			return nil, nil
		case "private.test":
			return []netip.Addr{netip.MustParseAddr("192.168.1.2")}, nil
		default:
			return nil, errors.New("resolver unavailable")
		}
	}

	parsed, ips, err := requireSafeURL("https://public.test/path?q=1")
	if err != nil {
		t.Fatalf("requireSafeURL public: %v", err)
	}
	if parsed.String() != "https://public.test/path?q=1" || len(ips) != 2 {
		t.Fatalf("public result = %v, %#v", parsed, ips)
	}
	if _, _, err := requireSafeURL("https://empty.test/feed"); err == nil || !strings.Contains(err.Error(), "could not resolve host") {
		t.Fatalf("empty resolver result: %v", err)
	}
	if _, _, err := requireSafeURL("https://private.test/feed"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("private resolver result: %v", err)
	}
	if _, _, err := requireSafeURL("https://unknown.test/feed"); err == nil || !strings.Contains(err.Error(), "could not resolve host") {
		t.Fatalf("resolver error result: %v", err)
	}
	if _, _, err := requireSafeURL("https://[::1"); err == nil || !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("parse error result: %v", err)
	}

	for _, rawURL := range []string{"example.test/feed", "http:///feed"} {
		if _, _, err := requireSafeURL(rawURL); err == nil || !strings.Contains(err.Error(), "URL") {
			t.Errorf("requireSafeURL(%q) error = %v", rawURL, err)
		}
	}
}

func TestPinnedDialerConnectsToPinnedAddress(t *testing.T) {
	if _, err := pinnedDialer(netip.MustParseAddr("127.0.0.1"))(context.Background(), "tcp", "missing-port"); err == nil {
		t.Fatal("expected malformed address to be rejected")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local sockets unavailable: %v", err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
		accepted <- err
	}()

	conn, err := pinnedDialer(netip.MustParseAddr("127.0.0.1"))(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	conn.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept pinned dial: %v", err)
	}
}

func TestGetUsesValidatedIPsRedirectsAndHandlesResponses(t *testing.T) {
	oldLookup, oldDo := lookupNetIP, doHTTP
	t.Cleanup(func() {
		lookupNetIP = oldLookup
		doHTTP = oldDo
	})
	lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	redirected := false
	doHTTP = func(client *http.Client, req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.Header.Get("User-Agent") != UserAgent {
			t.Errorf("request = method %s, user-agent %q", req.Method, req.Header.Get("User-Agent"))
		}
		redirectURL, err := url.Parse("https://redirect.test/next")
		if err != nil {
			return nil, err
		}
		redirectRequest := req.Clone(req.Context())
		redirectRequest.URL = redirectURL
		if err := client.CheckRedirect(redirectRequest, []*http.Request{req}); err != nil {
			return nil, err
		}
		redirected = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("validated response")),
		}, nil
	}

	resp, err := Get("https://public.test/start", time.Second)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "validated response" {
		t.Fatalf("response body = %q, err=%v", body, err)
	}
	if !redirected {
		t.Error("expected redirect validation callback")
	}
}

func TestGetPropagatesTransportAndHTTPStatusErrors(t *testing.T) {
	oldLookup, oldDo := lookupNetIP, doHTTP
	t.Cleanup(func() {
		lookupNetIP = oldLookup
		doHTTP = oldDo
	})
	lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}

	doHTTP = func(*http.Client, *http.Request) (*http.Response, error) {
		return nil, errors.New("transport failed")
	}
	if _, err := Get("https://public.test/transport", time.Second); err == nil || err.Error() != "transport failed" {
		t.Errorf("transport error = %v", err)
	}

	doHTTP = func(*http.Client, *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader("unavailable")),
		}, nil
	}
	resp, err := Get("https://public.test/status", time.Second)
	if resp != nil || err == nil || !IsRetryable(err) {
		t.Fatalf("status error: response=%v err=%v", resp, err)
	}
}

func TestGetRedirectGuards(t *testing.T) {
	oldLookup, oldDo := lookupNetIP, doHTTP
	t.Cleanup(func() {
		lookupNetIP = oldLookup
		doHTTP = oldDo
	})
	lookupNetIP = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host == "private.test" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}

	doHTTP = func(client *http.Client, req *http.Request) (*http.Response, error) {
		redirectURL, _ := url.Parse("https://public.test/loop")
		redirectRequest := req.Clone(req.Context())
		redirectRequest.URL = redirectURL
		if err := client.CheckRedirect(redirectRequest, make([]*http.Request, 10)); err == nil || err.Error() != "too many redirects" {
			t.Errorf("too many redirects error = %v", err)
		}
		privateURL, _ := url.Parse("https://private.test/redirect")
		redirectRequest.URL = privateURL
		if err := client.CheckRedirect(redirectRequest, []*http.Request{req}); err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("private redirect error = %v", err)
		}
		return nil, errors.New("redirect rejected")
	}
	if _, err := Get("https://public.test/start", time.Second); err == nil || err.Error() != "redirect rejected" {
		t.Errorf("Get redirect guard error = %v", err)
	}
}

func TestCheckResponseAndReadCappedHandleNilAndReaderErrors(t *testing.T) {
	if err := checkResponse(nil); err == nil || err.Error() != "empty HTTP response" {
		t.Errorf("checkResponse(nil) = %v", err)
	}
	if _, err := ReadCapped(errorReader{}, 20); err == nil || err.Error() != "reader failed" {
		t.Errorf("ReadCapped reader error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("reader failed") }

// newRepeatReader wraps a byte slice as a one-shot io.Reader for tests.
func newRepeatReader(data []byte) *sliceReader {
	return &sliceReader{data: data}
}

type sliceReader struct {
	data []byte
	pos  int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.pos:])
	s.pos += n
	return n, nil
}
