// Package safefetch performs HTTP(S) GET requests while rejecting requests to
// non-public addresses (SSRF) and pinning each connection to the address that
// was actually validated, so a DNS-rebinding host cannot swap in a private
// address between validation and connect.
package safefetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const UserAgent = "io.github.kitsunesemcalda.feader-rss/0.2"

var allowedSchemes = map[string]bool{"http": true, "https": true}

// ErrResponseTooLarge is returned by ReadCapped when the response exceeds the limit.
type ErrResponseTooLarge struct{ Limit int64 }

func (e *ErrResponseTooLarge) Error() string {
	return fmt.Sprintf("response exceeds %d byte limit", e.Limit)
}

func isGlobalIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !(addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() ||
		isReserved(addr))
}

// isReserved covers IANA special-purpose ranges not caught by netip's own
// helpers (e.g. 0.0.0.0/8, 240.0.0.0/4, the IPv6 documentation/6to4 ranges).
func isReserved(addr netip.Addr) bool {
	reserved := []string{
		"0.0.0.0/8",
		"100.64.0.0/10", // CGNAT
		"192.0.0.0/24",
		"192.0.2.0/24", // TEST-NET-1
		"198.18.0.0/15",
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",
		"::/128",
		"100::/64", // discard-only
		"2001:db8::/32",
		"2002::/16", // 6to4
	}
	for _, cidr := range reserved {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue
		}
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// requireSafeURL validates the URL's scheme and hostname, resolves it, and
// returns the resolved addresses. It errors if any resolved address is not
// publicly routable.
func requireSafeURL(rawURL string) (*url.URL, []netip.Addr, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}
	scheme := parsed.Scheme
	if !allowedSchemes[scheme] {
		if scheme == "" {
			scheme = "(none)"
		}
		return nil, nil, fmt.Errorf("unsupported URL scheme: %s", scheme)
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return nil, nil, fmt.Errorf("URL is missing a hostname")
	}

	ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", hostname)
	if err != nil {
		return nil, nil, fmt.Errorf("could not resolve host: %s", hostname)
	}
	if len(ips) == 0 {
		return nil, nil, fmt.Errorf("could not resolve host: %s", hostname)
	}
	for _, ip := range ips {
		if !isGlobalIP(ip) {
			return nil, nil, fmt.Errorf("refusing to contact non-public address: %s", ip)
		}
	}
	return parsed, ips, nil
}

// pinnedDialer returns a dial function that always connects to pinnedIP,
// ignoring whatever host net/http would otherwise resolve, while leaving
// TLS SNI/verification (handled by http.Transport) keyed on the hostname.
func pinnedDialer(pinnedIP netip.Addr) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: 20 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP.String(), port))
	}
}

// Get validates url, pins the connection to the validated address, performs
// an HTTP GET, and returns the response. Callers must close resp.Body. Every
// redirect target is re-validated and the connection is re-pinned to its
// address before following it.
func Get(rawURL string, timeout time.Duration) (*http.Response, error) {
	parsed, ips, err := requireSafeURL(rawURL)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		DialContext:     pinnedDialer(ips[0]),
		TLSClientConfig: &tls.Config{},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			_, redirIPs, err := requireSafeURL(req.URL.String())
			if err != nil {
				return err
			}
			transport.DialContext = pinnedDialer(redirIPs[0])
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	return client.Do(req)
}

// ReadCapped reads at most limit+1 bytes from r, returning an error instead
// of silently truncating if the response exceeds limit.
func ReadCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &ErrResponseTooLarge{Limit: limit}
	}
	return data, nil
}
