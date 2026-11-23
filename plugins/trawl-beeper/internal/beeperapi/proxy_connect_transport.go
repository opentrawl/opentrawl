package beeperapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewProxyConnectTunnelHTTPClient adapts OpenTrawl's injected loopback CONNECT
// proxy HTTP client so plain http:// requests (such as Beeper Desktop's local
// API on http://127.0.0.1:23373) also tunnel through HTTP CONNECT.
//
// Go's standard http.Transport uses HTTP CONNECT only for https:// targets and
// sends plain proxy GET requests for http:// targets, which OpenTrawl's
// AllowlistConnectProxy rejects with 405 Method Not Allowed. Tunneling every
// target via DialContext + HTTP CONNECT preserves the host proxy's exact
// Proxy-Authorization, host:port allowlist, and CIDR enforcement.
func NewProxyConnectTunnelHTTPClient(baseClient *http.Client, requestTimeout time.Duration) *http.Client {
	if requestTimeout <= 0 {
		requestTimeout = DefaultBeeperRequestTimeout
	}
	if baseClient == nil {
		return &http.Client{Timeout: requestTimeout}
	}
	proxyURL := extractConfiguredProxyURL(baseClient)
	if proxyURL == nil {
		cloned := *baseClient
		cloned.Timeout = requestTimeout
		return &cloned
	}

	proxyAddr := strings.TrimSpace(proxyURL.Host)
	proxyAuthHeader := buildProxyAuthorizationHeader(proxyURL.User)
	dialer := &net.Dialer{Timeout: requestTimeout}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network string, targetAddr string) (net.Conn, error) {
		return dialViaHTTPConnectProxy(ctx, dialer, proxyAddr, proxyAuthHeader, targetAddr)
	}
	return &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
	}
}

func extractConfiguredProxyURL(client *http.Client) *url.URL {
	if client == nil || client.Transport == nil {
		return nil
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		return nil
	}
	probeReq, err := http.NewRequest(http.MethodGet, "https://127.0.0.1/", nil)
	if err != nil {
		return nil
	}
	proxyURL, err := transport.Proxy(probeReq)
	if err != nil || proxyURL == nil || strings.TrimSpace(proxyURL.Host) == "" {
		return nil
	}
	return proxyURL
}

func buildProxyAuthorizationHeader(userInfo *url.Userinfo) string {
	if userInfo == nil {
		return ""
	}
	password, _ := userInfo.Password()
	encoded := base64.StdEncoding.EncodeToString([]byte(userInfo.Username() + ":" + password))
	return "Basic " + encoded
}

func dialViaHTTPConnectProxy(
	ctx context.Context,
	dialer *net.Dialer,
	proxyAddr string,
	proxyAuthHeader string,
	targetAddr string,
) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("dial OpenTrawl plugin proxy %s: %w", proxyAddr, err)
	}
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		_ = conn.SetDeadline(deadline)
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopCancel()

	var requestText strings.Builder
	requestText.WriteString("CONNECT ")
	requestText.WriteString(targetAddr)
	requestText.WriteString(" HTTP/1.1\r\nHost: ")
	requestText.WriteString(targetAddr)
	requestText.WriteString("\r\n")
	if proxyAuthHeader != "" {
		requestText.WriteString("Proxy-Authorization: ")
		requestText.WriteString(proxyAuthHeader)
		requestText.WriteString("\r\n")
	}
	requestText.WriteString("\r\n")

	if _, err := conn.Write([]byte(requestText.String())); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write CONNECT tunnel request for %s: %w", targetAddr, err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read CONNECT tunnel response for %s: %w", targetAddr, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("plugin proxy rejected CONNECT to %s with HTTP %d", targetAddr, resp.StatusCode)
	}

	_ = conn.SetDeadline(time.Time{})
	return &bufferedProxyTunnelConn{Conn: conn, reader: reader}, nil
}

type bufferedProxyTunnelConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyTunnelConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
