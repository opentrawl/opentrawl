// Package pluginproxy runs an ephemeral, per-invocation loopback HTTP CONNECT
// proxy that enforces exact host:port allowlists and blocks SSRF/DNS-rebinding
// to loopback, private, link-local, and CGNAT addresses unless explicitly
// exempted by user-approved self-hosted host prefixes.
package pluginproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	proxyBasicUsername   = "trawl"
	proxyDialTimeout     = 15 * time.Second
	proxyShutdownTimeout = 2 * time.Second
)

var cgnatIPv4Prefix = netip.MustParsePrefix("100.64.0.0/10")

// SelfHostedNetworkHostExemption allows one exact hostname and port to resolve
// to IP addresses within the approved CIDR prefixes (for example, a Tailscale
// or LAN self-hosted service).
type SelfHostedNetworkHostExemption struct {
	HostAndPort       string
	AllowedIPPrefixes []netip.Prefix
}

// AllowlistConnectProxyOptions configures one ephemeral loopback CONNECT proxy.
type AllowlistConnectProxyOptions struct {
	AllowedPublicHosts []string
	SelfHostedHosts    []SelfHostedNetworkHostExemption
	LookupIPAddresses  func(ctx context.Context, host string) ([]netip.Addr, error)
}

// AllowlistConnectProxy is a running per-invocation HTTP CONNECT proxy bound to
// 127.0.0.1:<ephemeral-port>.
type AllowlistConnectProxy struct {
	listener             net.Listener
	server               *http.Server
	authToken            string
	expectedBasicAuth    string
	allowedPublicHosts   map[string]struct{}
	selfHostedExemptions map[string][]netip.Prefix
	lookupIPAddresses    func(ctx context.Context, host string) ([]netip.Addr, error)
	closeOnce            sync.Once
}

// StartAllowlistConnectProxy binds an ephemeral loopback HTTP CONNECT proxy
// with a fresh 256-bit random authentication token.
func StartAllowlistConnectProxy(opts AllowlistConnectProxyOptions) (*AllowlistConnectProxy, error) {
	allowedPublic := make(map[string]struct{}, len(opts.AllowedPublicHosts))
	for _, raw := range opts.AllowedPublicHosts {
		normalized, err := NormalizeHostAndPort(raw)
		if err != nil {
			return nil, fmt.Errorf("allowed public host %q: %w", raw, err)
		}
		allowedPublic[normalized] = struct{}{}
	}
	selfHosted := make(map[string][]netip.Prefix, len(opts.SelfHostedHosts))
	for _, exemption := range opts.SelfHostedHosts {
		normalized, err := NormalizeHostAndPort(exemption.HostAndPort)
		if err != nil {
			return nil, fmt.Errorf("self-hosted host %q: %w", exemption.HostAndPort, err)
		}
		if len(exemption.AllowedIPPrefixes) == 0 {
			return nil, fmt.Errorf("self-hosted host %q must specify at least one allowed IP prefix", normalized)
		}
		selfHosted[normalized] = append([]netip.Prefix(nil), exemption.AllowedIPPrefixes...)
	}
	if len(allowedPublic) == 0 && len(selfHosted) == 0 {
		return nil, errors.New("connect proxy requires at least one allowlisted host")
	}

	token, err := generateRandomHexToken(32)
	if err != nil {
		return nil, err
	}
	lookup := opts.LookupIPAddresses
	if lookup == nil {
		lookup = defaultLookupIPAddresses
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen loopback connect proxy: %w", err)
	}

	credentials := base64.StdEncoding.EncodeToString([]byte(proxyBasicUsername + ":" + token))
	proxy := &AllowlistConnectProxy{
		listener:             listener,
		authToken:            token,
		expectedBasicAuth:    "Basic " + credentials,
		allowedPublicHosts:   allowedPublic,
		selfHostedExemptions: selfHosted,
		lookupIPAddresses:    lookup,
	}
	proxy.server = &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = proxy.server.Serve(listener)
	}()
	return proxy, nil
}

// Address returns the bound 127.0.0.1:<port> address.
func (proxy *AllowlistConnectProxy) Address() string {
	return proxy.listener.Addr().String()
}

// Port returns the bound loopback TCP port number.
func (proxy *AllowlistConnectProxy) Port() int {
	if tcpAddr, ok := proxy.listener.Addr().(*net.TCPAddr); ok {
		return tcpAddr.Port
	}
	return 0
}

// AuthToken returns the per-invocation authentication token that clients must
// supply in Proxy-Authorization.
func (proxy *AllowlistConnectProxy) AuthToken() string {
	return proxy.authToken
}

// Close shuts down the loopback listener and server.
func (proxy *AllowlistConnectProxy) Close() error {
	var closeErr error
	proxy.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), proxyShutdownTimeout)
		defer cancel()
		closeErr = proxy.server.Shutdown(ctx)
	})
	return closeErr
}

// ServeHTTP handles HTTP CONNECT tunnels and rejects all other HTTP methods.
func (proxy *AllowlistConnectProxy) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	if !proxy.authorized(request.Header.Get("Proxy-Authorization")) {
		responseWriter.Header().Set("Proxy-Authenticate", `Basic realm="OpenTrawl"`)
		http.Error(responseWriter, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method != http.MethodConnect {
		http.Error(responseWriter, "only HTTP CONNECT is allowed", http.StatusMethodNotAllowed)
		return
	}
	normalizedTarget, err := NormalizeHostAndPort(request.Host)
	if err != nil {
		http.Error(responseWriter, "invalid CONNECT target", http.StatusBadRequest)
		return
	}
	targetAddress, err := proxy.resolvePermittedDialAddress(request.Context(), normalizedTarget)
	if err != nil {
		http.Error(responseWriter, err.Error(), http.StatusForbidden)
		return
	}
	upstreamConn, err := (&net.Dialer{Timeout: proxyDialTimeout}).DialContext(request.Context(), "tcp", targetAddress)
	if err != nil {
		http.Error(responseWriter, "upstream dial failed", http.StatusBadGateway)
		return
	}
	hijacker, ok := responseWriter.(http.Hijacker)
	if !ok {
		_ = upstreamConn.Close()
		http.Error(responseWriter, "proxy hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuffer, err := hijacker.Hijack()
	if err != nil {
		_ = upstreamConn.Close()
		return
	}
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = upstreamConn.Close()
		_ = clientConn.Close()
		return
	}
	tunnelBidirectionalConnections(clientConn, clientBuffer.Reader, upstreamConn)
}

func (proxy *AllowlistConnectProxy) authorized(headerValue string) bool {
	return subtle.ConstantTimeCompare([]byte(headerValue), []byte(proxy.expectedBasicAuth)) == 1
}

func (proxy *AllowlistConnectProxy) resolvePermittedDialAddress(ctx context.Context, normalizedTarget string) (string, error) {
	_, publicAllowed := proxy.allowedPublicHosts[normalizedTarget]
	exemptPrefixes, selfHostedAllowed := proxy.selfHostedExemptions[normalizedTarget]
	if !publicAllowed && !selfHostedAllowed {
		return "", fmt.Errorf("destination %q is not in plugin network allowlist", normalizedTarget)
	}
	host, port, err := net.SplitHostPort(normalizedTarget)
	if err != nil {
		return "", err
	}
	resolvedAddrs, err := proxy.lookupHostAddresses(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve destination %q: %w", host, err)
	}
	for _, addr := range resolvedAddrs {
		unmapped := addr.Unmap()
		if isPermittedDestinationAddr(unmapped, publicAllowed, exemptPrefixes) {
			return net.JoinHostPort(unmapped.String(), port), nil
		}
	}
	return "", fmt.Errorf("destination %q resolved only to disallowed IP addresses", normalizedTarget)
}

func (proxy *AllowlistConnectProxy) lookupHostAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if literalAddr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literalAddr}, nil
	}
	addrs, err := proxy.lookupIPAddresses(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errors.New("no IP addresses returned")
	}
	return addrs, nil
}

func isPermittedDestinationAddr(addr netip.Addr, publicAllowed bool, exemptPrefixes []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, prefix := range exemptPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	if !publicAllowed {
		return false
	}
	return !isNonPublicAddr(addr)
}

func isNonPublicAddr(addr netip.Addr) bool {
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() ||
		addr.IsInterfaceLocalMulticast() {
		return true
	}
	return addr.Is4() && cgnatIPv4Prefix.Contains(addr)
}

func tunnelBidirectionalConnections(clientConn net.Conn, clientReader io.Reader, upstreamConn net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstreamConn, clientReader)
		closeWriteSide(upstreamConn)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, upstreamConn)
		closeWriteSide(clientConn)
	}()
	wg.Wait()
	_ = upstreamConn.Close()
	_ = clientConn.Close()
}

func closeWriteSide(conn net.Conn) {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

func defaultLookupIPAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	ipAddrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ipAddrs))
	for _, ipAddr := range ipAddrs {
		if parsed, ok := netip.AddrFromSlice(ipAddr.IP); ok {
			addrs = append(addrs, parsed.Unmap())
		}
	}
	return addrs, nil
}

// NormalizeHostAndPort validates and canonicalizes an exact host:port entry.
func NormalizeHostAndPort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "", errors.New("must be exact host:port")
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || strings.ContainsAny(host, "* \t\r\n/") {
		return "", errors.New("hostname is invalid or contains wildcard")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ParseIPPrefixes parses CIDR prefixes or single IP addresses into netip.Prefix
// values for self-hosted network host exemptions.
func ParseIPPrefixes(rawPrefixes []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(rawPrefixes))
	for _, raw := range rawPrefixes {
		trimmed := strings.TrimSpace(raw)
		if strings.Contains(trimmed, "/") {
			prefix, err := netip.ParsePrefix(trimmed)
			if err != nil {
				return nil, fmt.Errorf("invalid IP prefix %q: %w", trimmed, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid IP address %q: %w", trimmed, err)
		}
		unmapped := addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(unmapped, unmapped.BitLen()))
	}
	return prefixes, nil
}

// NewProxyHTTPClient returns an http.Client configured to tunnel all requests
// through the host's authenticated loopback HTTP CONNECT proxy.
func NewProxyHTTPClient(proxyAddress string, proxyAuthToken string, timeout time.Duration) (*http.Client, error) {
	if strings.TrimSpace(proxyAddress) == "" || strings.TrimSpace(proxyAuthToken) == "" {
		return nil, errors.New("proxy address and auth token are required")
	}
	proxyURL := &url.URL{
		Scheme: "http",
		User:   url.UserPassword(proxyBasicUsername, proxyAuthToken),
		Host:   strings.TrimSpace(proxyAddress),
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}

func generateRandomHexToken(byteCount int) (string, error) {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate proxy auth token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
