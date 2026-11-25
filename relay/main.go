package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/caddyserver/certmagic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	plugin "github.com/fatedier/frp/pkg/plugin/server"
	"github.com/fatedier/frp/server"
	"go.uber.org/zap"
)

const (
	relayDomainHashContext         = "opentrawl-mcp-domain-v1|"
	relayLoginSignContext          = "opentrawl-relay-login-v1|"
	relayLoginSkewSeconds          = 60
	relayACMEContactEmail          = "tls@opentrawl.sh"
	relayProxyNamePrefix           = "opentrawl-"
	relayMaxConcurrentSNIConns     = 512
	relayMaxConcurrentControlConns = 256
	relaySpliceIdleTimeout         = 5 * time.Minute
	relayTCPKeepAlivePeriod        = 30 * time.Second
	relayAcceptTransientBackoff    = 50 * time.Millisecond
)

type relayPluginRequestEnvelope struct {
	Version string          `json:"version"`
	Op      string          `json:"op"`
	Content json.RawMessage `json:"content"`
}

type relayAdmissionConfig struct {
	domainSuffix                string
	inlineAllowedKeys           map[string]struct{}
	allowedKeysFile             string
	allowUnrestrictedEnrollment bool
	bandwidthLimit              string
}

type relayServerOptions struct {
	bindAddr                    string
	bindPort                    int
	httpsPort                   int
	baseDomain                  string
	controlDomain               string
	tlsStateDir                 string
	tlsCertFile                 string
	tlsKeyFile                  string
	allowedPubKeysCSV           string
	allowedPubKeysFile          string
	allowUnrestrictedEnrollment bool
	bandwidthLimit              string
	allowInsecureControlTL      bool
}

func deriveDeviceSubdomain(publicKey ed25519.PublicKey) string {
	digest := sha256.New()
	digest.Write([]byte(relayDomainHashContext))
	digest.Write(publicKey)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest.Sum(nil))
	return strings.ToLower(encoded[:20])
}

func parseAllowedPubKeysCSV(raw string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, entry := range strings.Split(raw, ",") {
		cleaned := strings.ToLower(strings.TrimSpace(entry))
		if cleaned != "" {
			allowed[cleaned] = struct{}{}
		}
	}
	return allowed
}

func (cfg relayAdmissionConfig) isPubKeyAllowed(pubHexLower string) (bool, error) {
	hasGate := len(cfg.inlineAllowedKeys) > 0 || strings.TrimSpace(cfg.allowedKeysFile) != ""
	if !hasGate {
		return cfg.allowUnrestrictedEnrollment, nil
	}
	if _, ok := cfg.inlineAllowedKeys[pubHexLower]; ok {
		return true, nil
	}
	filePath := strings.TrimSpace(cfg.allowedKeysFile)
	if filePath == "" {
		return false, nil
	}
	// #nosec G304 -- path comes from explicit operator CLI flag --allowed-pubkeys-file.
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 && strings.ToLower(fields[0]) == pubHexLower {
			return true, nil
		}
	}
	return false, nil
}

func startStatelessEd25519AuthPlugin(admission relayAdmissionConfig) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/frp-plugin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var envelope relayPluginRequestEnvelope
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&envelope); err != nil {
			writePluginResponse(w, plugin.Response{Reject: true, RejectReason: "invalid plugin request"})
			return
		}
		writePluginResponse(w, evaluateRelayPluginOperation(envelope, admission, time.Now()))
	})
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return listener.Addr().String(), func() { _ = httpServer.Close() }, nil
}

func writePluginResponse(w http.ResponseWriter, resp plugin.Response) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func evaluateRelayPluginOperation(envelope relayPluginRequestEnvelope, admission relayAdmissionConfig, now time.Time) plugin.Response {
	switch envelope.Op {
	case "Login":
		var content plugin.LoginContent
		if err := json.Unmarshal(envelope.Content, &content); err != nil {
			return plugin.Response{Reject: true, RejectReason: "invalid login payload"}
		}
		pubHex := strings.ToLower(strings.TrimSpace(content.Metas["pubkey"]))
		tsText := strings.TrimSpace(content.Metas["ts"])
		sigHex := strings.TrimSpace(content.Metas["sig"])
		pubBytes, errPub := hex.DecodeString(pubHex)
		sigBytes, errSig := hex.DecodeString(sigHex)
		tsSeconds, errTS := strconv.ParseInt(tsText, 10, 64)
		if errPub != nil || errSig != nil || errTS != nil || len(pubBytes) != ed25519.PublicKeySize || len(sigBytes) != ed25519.SignatureSize {
			return plugin.Response{Reject: true, RejectReason: "missing or invalid ed25519 login credentials"}
		}
		nowUnix := now.UTC().Unix()
		if tsSeconds < nowUnix-relayLoginSkewSeconds || tsSeconds > nowUnix+relayLoginSkewSeconds {
			return plugin.Response{Reject: true, RejectReason: "login timestamp outside allowed window"}
		}
		if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte(relayLoginSignContext+pubHex+"|"+tsText), sigBytes) {
			return plugin.Response{Reject: true, RejectReason: "ed25519 login signature verification failed"}
		}
		allowed, err := admission.isPubKeyAllowed(pubHex)
		if err != nil {
			return plugin.Response{Reject: true, RejectReason: "relay admission list could not be read"}
		}
		if !allowed {
			return plugin.Response{Reject: true, RejectReason: "device key is not enrolled on this relay"}
		}
		return plugin.Response{Unchange: true}

	case "NewProxy":
		var content plugin.NewProxyContent
		if err := json.Unmarshal(envelope.Content, &content); err != nil {
			return plugin.Response{Reject: true, RejectReason: "invalid proxy payload"}
		}
		if content.ProxyType != "https" {
			return plugin.Response{Reject: true, RejectReason: "only https SNI passthrough proxies are permitted"}
		}
		if content.SubDomain != "" || len(content.CustomDomains) != 1 {
			return plugin.Response{Reject: true, RejectReason: "exactly one key-bound custom domain is required"}
		}
		pubBytes, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(content.User.Metas["pubkey"])))
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			return plugin.Response{Reject: true, RejectReason: "verified pubkey missing"}
		}
		expectedSubdomain := deriveDeviceSubdomain(ed25519.PublicKey(pubBytes))
		expectedProxyName := relayProxyNamePrefix + expectedSubdomain
		if content.ProxyName != expectedProxyName {
			return plugin.Response{Reject: true, RejectReason: "proxy name does not match device key"}
		}
		expectedDomain := expectedSubdomain + admission.domainSuffix
		if content.CustomDomains[0] != expectedDomain {
			return plugin.Response{Reject: true, RejectReason: "requested domain does not match device key"}
		}
		if limit := strings.TrimSpace(admission.bandwidthLimit); limit != "" {
			content.BandwidthLimit = limit
			content.BandwidthLimitMode = "server"
			return plugin.Response{Unchange: false, Content: content}
		}
		return plugin.Response{Unchange: true}

	default:
		return plugin.Response{Reject: true, RejectReason: "unsupported plugin operation"}
	}
}

func reserveEphemeralLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

type prefixConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type readOnlyConn struct {
	reader io.Reader
}

func (c *readOnlyConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *readOnlyConn) Write(p []byte) (int, error)      { return 0, io.ErrClosedPipe }
func (c *readOnlyConn) Close() error                     { return nil }
func (c *readOnlyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *readOnlyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *readOnlyConn) SetDeadline(time.Time) error      { return nil }
func (c *readOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (c *readOnlyConn) SetWriteDeadline(time.Time) error { return nil }

func peekTLSClientHelloSNI(conn net.Conn, timeout time.Duration) (string, net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var peeked bytes.Buffer
	var serverName string
	tlsConn := tls.Server(&readOnlyConn{reader: io.TeeReader(io.LimitReader(conn, 16<<10), &peeked)}, &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			serverName = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hello.ServerName)), ".")
			return nil, errors.New("peek complete")
		},
	})
	_ = tlsConn.Handshake()
	_ = conn.SetReadDeadline(time.Time{})
	return serverName, &prefixConn{
		Conn:   conn,
		reader: io.MultiReader(bytes.NewReader(peeked.Bytes()), conn),
	}
}

func configureRelayTCPKeepAlive(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(relayTCPKeepAlivePeriod)
	}
}

func copyWithIdleDeadline(dst net.Conn, src net.Conn, idleTimeout time.Duration) {
	buf := make([]byte, 32<<10)
	for {
		_ = src.SetReadDeadline(time.Now().Add(idleTimeout))
		n, readErr := src.Read(buf)
		if n > 0 {
			_ = dst.SetWriteDeadline(time.Now().Add(idleTimeout))
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}

func spliceTCPConnections(clientConn net.Conn, targetAddr string) {
	defer func() { _ = clientConn.Close() }()
	configureRelayTCPKeepAlive(clientConn)
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: relayTCPKeepAlivePeriod,
	}
	backendConn, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		return
	}
	defer func() { _ = backendConn.Close() }()
	configureRelayTCPKeepAlive(backendConn)

	done := make(chan struct{}, 2)
	go func() {
		copyWithIdleDeadline(backendConn, clientConn, relaySpliceIdleTimeout)
		if tcp, ok := backendConn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		copyWithIdleDeadline(clientConn, backendConn, relaySpliceIdleTimeout)
		if closer, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func startSNIPassthroughMux(
	ctx context.Context,
	listenAddr string,
	controlDomain string,
	controlACMEAddr string,
	frpsHTTPSAddr string,
) (func(), error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	normalizedControlDomain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(controlDomain)), ".")
	slots := make(chan struct{}, relayMaxConcurrentSNIConns)
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(relayAcceptTransientBackoff):
				}
				continue
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			go func(c net.Conn) {
				defer func() { <-slots }()
				sni, replayed := peekTLSClientHelloSNI(c, 5*time.Second)
				target := frpsHTTPSAddr
				if normalizedControlDomain != "" && sni == normalizedControlDomain && controlACMEAddr != "" {
					target = controlACMEAddr
				}
				spliceTCPConnections(replayed, target)
			}(conn)
		}
	}()
	return func() { _ = listener.Close() }, nil
}

func startTLSControlPlaneForwarder(
	ctx context.Context,
	listenAddr string,
	tlsConfig *tls.Config,
	frpsBindAddr string,
) (func(), error) {
	rawListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	tlsListener := tls.NewListener(rawListener, tlsConfig)
	slots := make(chan struct{}, relayMaxConcurrentControlConns)
	go func() {
		<-ctx.Done()
		_ = tlsListener.Close()
	}()
	go func() {
		for {
			conn, err := tlsListener.Accept()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(relayAcceptTransientBackoff):
				}
				continue
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			go func(c net.Conn) {
				defer func() { <-slots }()
				spliceTCPConnections(c, frpsBindAddr)
			}(conn)
		}
	}()
	return func() { _ = tlsListener.Close() }, nil
}

func runRelayServer(ctx context.Context, opts relayServerOptions) error {
	domainSuffix := "." + strings.TrimPrefix(strings.TrimSpace(opts.baseDomain), ".")
	inlineKeys := parseAllowedPubKeysCSV(opts.allowedPubKeysCSV)
	keysFile := strings.TrimSpace(opts.allowedPubKeysFile)
	if len(inlineKeys) == 0 && keysFile == "" && !opts.allowUnrestrictedEnrollment {
		return errors.New("enrollment gate required: pass --allowed-pubkeys or --allowed-pubkeys-file, or explicitly pass --allow-unrestricted-enrollment")
	}
	if keysFile != "" {
		// #nosec G304 -- path comes from explicit operator CLI flag --allowed-pubkeys-file.
		if _, err := os.ReadFile(keysFile); err != nil {
			return errors.New("cannot read --allowed-pubkeys-file: " + err.Error())
		}
	}
	admission := relayAdmissionConfig{
		domainSuffix:                domainSuffix,
		inlineAllowedKeys:           inlineKeys,
		allowedKeysFile:             keysFile,
		allowUnrestrictedEnrollment: opts.allowUnrestrictedEnrollment,
		bandwidthLimit:              strings.TrimSpace(opts.bandwidthLimit),
	}
	pluginAddr, stopPlugin, err := startStatelessEd25519AuthPlugin(admission)
	if err != nil {
		return err
	}
	defer stopPlugin()

	if opts.allowInsecureControlTL {
		serverConfig := &v1.ServerConfig{
			BindAddr:       opts.bindAddr,
			BindPort:       opts.bindPort,
			VhostHTTPSPort: opts.httpsPort,
			HTTPPlugins: []v1.HTTPPluginOptions{
				{
					Name: "opentrawl-ed25519-domain-bind",
					Addr: pluginAddr,
					Path: "/frp-plugin",
					Ops:  []string{"Login", "NewProxy"},
				},
			},
		}
		if err := serverConfig.Complete(); err != nil {
			return err
		}
		frpService, err := server.NewService(serverConfig)
		if err != nil {
			return err
		}
		defer func() { _ = frpService.Close() }()
		frpService.Run(ctx)
		return nil
	}

	internalBindPort, err := reserveEphemeralLoopbackPort()
	if err != nil {
		return err
	}
	internalHTTPSPort, err := reserveEphemeralLoopbackPort()
	if err != nil {
		return err
	}

	serverConfig := &v1.ServerConfig{
		BindAddr:       "127.0.0.1",
		BindPort:       internalBindPort,
		VhostHTTPSPort: internalHTTPSPort,
		HTTPPlugins: []v1.HTTPPluginOptions{
			{
				Name: "opentrawl-ed25519-domain-bind",
				Addr: pluginAddr,
				Path: "/frp-plugin",
				Ops:  []string{"Login", "NewProxy"},
			},
		},
	}
	if err := serverConfig.Complete(); err != nil {
		return err
	}
	frpService, err := server.NewService(serverConfig)
	if err != nil {
		return err
	}
	defer func() { _ = frpService.Close() }()
	go frpService.Run(ctx)

	frpsBindAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(internalBindPort))
	frpsHTTPSAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(internalHTTPSPort))
	publicHTTPSAddr := net.JoinHostPort(opts.bindAddr, strconv.Itoa(opts.httpsPort))
	publicControlAddr := net.JoinHostPort(opts.bindAddr, strconv.Itoa(opts.bindPort))

	var controlTLSConfig *tls.Config
	certFile := strings.TrimSpace(opts.tlsCertFile)
	keyFile := strings.TrimSpace(opts.tlsKeyFile)
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return errors.New("both --tls-cert and --tls-key are required")
		}
		cert, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
		if loadErr != nil {
			return loadErr
		}
		controlTLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}
		stopMux, muxErr := startSNIPassthroughMux(ctx, publicHTTPSAddr, "", "", frpsHTTPSAddr)
		if muxErr != nil {
			return muxErr
		}
		defer stopMux()
	} else {
		controlDomain := strings.TrimSpace(opts.controlDomain)
		if controlDomain == "" {
			return errors.New("--control-domain (or --tls-cert/--tls-key) is required unless --allow-insecure-control-tls is set")
		}
		tlsStateDir := strings.TrimSpace(opts.tlsStateDir)
		if tlsStateDir == "" {
			tlsStateDir = "./relay-tls"
		}
		if mkErr := os.MkdirAll(filepath.Clean(tlsStateDir), 0o700); mkErr != nil {
			return mkErr
		}
		alpnPort, portErr := reserveEphemeralLoopbackPort()
		if portErr != nil {
			return portErr
		}
		stopMux, muxErr := startSNIPassthroughMux(
			ctx,
			publicHTTPSAddr,
			controlDomain,
			net.JoinHostPort("127.0.0.1", strconv.Itoa(alpnPort)),
			frpsHTTPSAddr,
		)
		if muxErr != nil {
			return muxErr
		}
		defer stopMux()

		var certConfig *certmagic.Config
		certCache := certmagic.NewCache(certmagic.CacheOptions{
			GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
				return certConfig, nil
			},
			Logger: zap.NewNop(),
		})
		defer certCache.Stop()
		certConfig = certmagic.New(certCache, certmagic.Config{
			Storage: &certmagic.FileStorage{Path: tlsStateDir},
			Logger:  zap.NewNop(),
		})
		certConfig.Issuers = []certmagic.Issuer{
			certmagic.NewACMEIssuer(certConfig, certmagic.ACMEIssuer{
				CA:                      certmagic.LetsEncryptProductionCA,
				Email:                   relayACMEContactEmail,
				Agreed:                  true,
				DisableHTTPChallenge:    true,
				DisableTLSALPNChallenge: false,
				ListenHost:              "127.0.0.1",
				AltTLSALPNPort:          alpnPort,
				Logger:                  zap.NewNop(),
			}),
			certmagic.NewACMEIssuer(certConfig, certmagic.ACMEIssuer{
				CA:                      certmagic.ZeroSSLProductionCA,
				Email:                   relayACMEContactEmail,
				Agreed:                  true,
				DisableHTTPChallenge:    true,
				DisableTLSALPNChallenge: false,
				ListenHost:              "127.0.0.1",
				AltTLSALPNPort:          alpnPort,
				Logger:                  zap.NewNop(),
			}),
		}
		if manageErr := certConfig.ManageSync(ctx, []string{controlDomain}); manageErr != nil {
			return errors.New("control-plane TLS certificate setup failed: " + manageErr.Error())
		}
		controlTLSConfig = certConfig.TLSConfig()
		controlTLSConfig.MinVersion = tls.VersionTLS12
		controlTLSConfig.NextProtos = nil
	}

	stopControl, err := startTLSControlPlaneForwarder(ctx, publicControlAddr, controlTLSConfig, frpsBindAddr)
	if err != nil {
		return err
	}
	defer stopControl()

	<-ctx.Done()
	return nil
}

func main() {
	opts := relayServerOptions{}
	flag.StringVar(&opts.bindAddr, "bind-addr", "0.0.0.0", "Address for TLS control plane and HTTPS SNI passthrough")
	flag.IntVar(&opts.bindPort, "bind-port", 7000, "TCP port for TLS-encrypted frpc control plane")
	flag.IntVar(&opts.httpsPort, "https-port", 443, "TCP port for raw TLS SNI passthrough")
	flag.StringVar(&opts.baseDomain, "domain", "mcp.opentrawl.sh", "Base domain for key-bound subdomains")
	flag.StringVar(&opts.controlDomain, "control-domain", "relay.mcp.opentrawl.sh", "Control-plane hostname for automatic Let's Encrypt TLS")
	flag.StringVar(&opts.tlsStateDir, "tls-state-dir", "./relay-tls", "CertMagic storage directory for control-plane TLS")
	flag.StringVar(&opts.tlsCertFile, "tls-cert", "", "Explicit PEM certificate for control-plane TLS (pair with --tls-key)")
	flag.StringVar(&opts.tlsKeyFile, "tls-key", "", "Explicit PEM private key for control-plane TLS (pair with --tls-cert)")
	flag.StringVar(&opts.allowedPubKeysCSV, "allowed-pubkeys", "", "Comma-separated 64-hex Ed25519 pubkeys permitted to connect")
	flag.StringVar(&opts.allowedPubKeysFile, "allowed-pubkeys-file", "", "File of enrolled 64-hex Ed25519 pubkeys (reloaded per login)")
	flag.BoolVar(&opts.allowUnrestrictedEnrollment, "allow-unrestricted-enrollment", false, "Allow any Ed25519 key to enroll without an allowlist (unsafe for public relays)")
	flag.StringVar(&opts.bandwidthLimit, "bandwidth-limit", "10MB", "Per-proxy bandwidth limit enforced server-side")
	flag.BoolVar(&opts.allowInsecureControlTL, "allow-insecure-control-tls", false, "Disable mandatory TLS on control plane (local testing only)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runRelayServer(ctx, opts); err != nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}
