package routing

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	alwaysBlockedNets = []*net.IPNet{
		mustCIDR("0.0.0.0/8"),
		mustCIDR("169.254.0.0/16"), // link-local + cloud IMDS; never opted in
		mustCIDR("224.0.0.0/4"),
		mustCIDR("255.255.255.255/32"),
		mustCIDR("fe80::/10"),
		mustCIDR("ff00::/8"),
	}
	privateUnlessAllowedNets = []*net.IPNet{
		mustCIDR("10.0.0.0/8"),
		mustCIDR("172.16.0.0/12"),
		mustCIDR("192.168.0.0/16"),
		mustCIDR("127.0.0.0/8"),
		mustCIDR("100.64.0.0/10"),
		mustCIDR("::1/128"),
		mustCIDR("fc00::/7"),
	}
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ValidateGatewayURL checks scheme, credentials, host, and literal IPs.
// Hostname DNS is enforced later by GuardHTTPClient so Docker names like
// gateway-a can be registered, then resolved at request time (N-05).
func ValidateGatewayURL(raw string, allowPrivate bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("gateway url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("gateway url: %w", err)
	}
	return validateParsedURL(u, allowPrivate)
}

func validateParsedURL(u *url.URL, allowPrivate bool) error {
	if u == nil || u.Host == "" {
		return fmt.Errorf("gateway url must include a host")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("gateway url scheme %q is not allowed", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("gateway url must not contain userinfo")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("gateway url host is empty")
	}
	if err := checkHostname(host, allowPrivate); err != nil {
		return err
	}
	if ip := parseIPLiteral(host); ip != nil {
		return checkIP(ip, allowPrivate)
	}
	return nil
}

func checkHostname(host string, allowPrivate bool) error {
	h := strings.ToLower(strings.TrimSpace(host))
	switch h {
	case "metadata", "metadata.google.internal", "instance-data":
		return fmt.Errorf("gateway host %q is blocked", host)
	case "localhost", "localhost.localdomain":
		if !allowPrivate {
			return fmt.Errorf("gateway host %q is loopback; set ROUTING_ALLOW_PRIVATE=true for lab networks", host)
		}
	}
	return nil
}

func parseIPLiteral(host string) net.IP {
	return net.ParseIP(strings.Trim(host, "[]"))
}

func checkIP(ip net.IP, allowPrivate bool) error {
	if ip == nil {
		return fmt.Errorf("invalid ip")
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range alwaysBlockedNets {
		if n.Contains(ip) {
			return fmt.Errorf("gateway ip %s is blocked", ip)
		}
	}
	if allowPrivate {
		return nil
	}
	for _, n := range privateUnlessAllowedNets {
		if n.Contains(ip) {
			return fmt.Errorf("gateway ip %s is private; set ROUTING_ALLOW_PRIVATE=true", ip)
		}
	}
	return nil
}

func pickSafeIP(ctx context.Context, host string, allowPrivate bool) (net.IP, error) {
	if err := checkHostname(host, allowPrivate); err != nil {
		return nil, err
	}
	if ip := parseIPLiteral(host); ip != nil {
		if err := checkIP(ip, allowPrivate); err != nil {
			return nil, err
		}
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("gateway host %q resolve: %w", host, err)
	}
	var blocked error
	for _, a := range addrs {
		if err := checkIP(a.IP, allowPrivate); err != nil {
			if blocked == nil {
				blocked = err
			}
			continue
		}
		return a.IP, nil
	}
	if blocked != nil {
		return nil, blocked
	}
	return nil, fmt.Errorf("gateway host %q resolved to no usable addresses", host)
}

// GuardHTTPClient copies client and forces every dial (including redirects)
// through pickSafeIP so poisoned Redis URLs and DNS rebinding cannot reach
// IMDS, loopback admin, or RFC1918 hosts unless AllowPrivate is set.
func GuardHTTPClient(client *http.Client, allowPrivate bool) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	out := *client
	transport := cloneTransport(client.Transport)
	rootDial := transport.DialContext
	if rootDial == nil {
		d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		rootDial = d.DialContext
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := pickSafeIP(ctx, host, allowPrivate)
		if err != nil {
			return nil, err
		}
		return rootDial(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	out.Transport = transport
	prev := out.CheckRedirect
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateParsedURL(req.URL, allowPrivate); err != nil {
			return err
		}
		if _, err := pickSafeIP(req.Context(), req.URL.Hostname(), allowPrivate); err != nil {
			return err
		}
		if prev != nil {
			return prev(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	return &out
}

func cloneTransport(rt http.RoundTripper) *http.Transport {
	if t, ok := rt.(*http.Transport); ok && t != nil {
		return t.Clone()
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}
