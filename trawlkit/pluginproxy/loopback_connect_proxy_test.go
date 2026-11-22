package pluginproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAllowlistConnectProxyEnforcesAuthAllowlistAntiSSRFAndSelfHostedExemptions(t *testing.T) {
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	defer func() { _ = echoListener.Close() }()
	go func() {
		for {
			conn, acceptErr := echoListener.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	echoPort := echoListener.Addr().(*net.TCPAddr).Port
	selfHostedTarget := net.JoinHostPort("selfhosted.internal", strconv.Itoa(echoPort))
	publicOnlyTarget := net.JoinHostPort("rebound.internal", strconv.Itoa(echoPort))

	proxy, err := StartAllowlistConnectProxy(AllowlistConnectProxyOptions{
		AllowedPublicHosts: []string{publicOnlyTarget},
		SelfHostedHosts: []SelfHostedNetworkHostExemption{{
			HostAndPort:       selfHostedTarget,
			AllowedIPPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		}},
		LookupIPAddresses: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "selfhosted.internal", "rebound.internal":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			default:
				return []netip.Addr{netip.MustParseAddr("100.64.0.10")}, nil
			}
		},
	})
	if err != nil {
		t.Fatalf("StartAllowlistConnectProxy failed: %v", err)
	}
	defer func() { _ = proxy.Close() }()

	if status := sendRawConnect(t, proxy.Address(), selfHostedTarget, ""); status != http.StatusProxyAuthRequired {
		t.Fatalf("missing auth status = %d, want 407", status)
	}

	validAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("trawl:"+proxy.AuthToken()))

	if status := sendRawConnect(t, proxy.Address(), "unlisted.internal:443", validAuth); status != http.StatusForbidden {
		t.Fatalf("unallowlisted host status = %d, want 403", status)
	}

	if status := sendRawConnect(t, proxy.Address(), publicOnlyTarget, validAuth); status != http.StatusForbidden {
		t.Fatalf("DNS-rebound loopback IP on public-only host status = %d, want 403", status)
	}

	// Plain HTTP GET smuggling must be rejected with 405.
	plainClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{
				Scheme: "http",
				User:   url.UserPassword("trawl", proxy.AuthToken()),
				Host:   proxy.Address(),
			}),
		},
	}
	plainResp, err := plainClient.Get("http://" + selfHostedTarget + "/")
	if err != nil {
		t.Fatalf("plain HTTP request to proxy failed unexpectedly: %v", err)
	}
	_ = plainResp.Body.Close()
	if plainResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("non-CONNECT method status = %d, want 405", plainResp.StatusCode)
	}

	// Explicitly exempted self-hosted target must tunnel cleanly.
	conn, err := net.DialTimeout("tcp", proxy.Address(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	connectReq := "CONNECT " + selfHostedTarget + " HTTP/1.1\r\nHost: " + selfHostedTarget + "\r\nProxy-Authorization: " + validAuth + "\r\n\r\n"
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "200") {
		t.Fatalf("CONNECT response = %q (err=%v), want 200", statusLine, err)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read CONNECT headers: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write tunneled bytes: %v", err)
	}
	echoed, err := reader.ReadString('\n')
	if err != nil || echoed != "ping\n" {
		t.Fatalf("tunneled echo = %q (err=%v), want ping\\n", echoed, err)
	}
}

func TestIsNonPublicAddrBlocksPrivateAndTailscaleCGNAT(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "100.127.255.254", "::1", "fc00::1", "fe80::1"}
	for _, raw := range blocked {
		addr := netip.MustParseAddr(raw)
		if !isNonPublicAddr(addr) {
			t.Fatalf("expected %s to be treated as non-public", raw)
		}
	}
	if isNonPublicAddr(netip.MustParseAddr("1.1.1.1")) {
		t.Fatalf("expected public IP 1.1.1.1 to be permitted")
	}
}

func sendRawConnect(t *testing.T, proxyAddr string, target string, authHeader string) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var req strings.Builder
	req.WriteString("CONNECT ")
	req.WriteString(target)
	req.WriteString(" HTTP/1.1\r\nHost: ")
	req.WriteString(target)
	req.WriteString("\r\n")
	if authHeader != "" {
		req.WriteString("Proxy-Authorization: ")
		req.WriteString(authHeader)
		req.WriteString("\r\n")
	}
	req.WriteString("\r\n")
	if _, err := conn.Write([]byte(req.String())); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
