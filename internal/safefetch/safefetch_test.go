package safefetch

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
