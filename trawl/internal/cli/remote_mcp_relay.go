package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	frplog "github.com/fatedier/frp/pkg/util/log"
	"go.uber.org/zap"
)

const (
	remoteMCPRelayDefaultServerAddress = "relay.mcp.opentrawl.sh:7000"
	remoteMCPRelayDefaultBaseDomain    = "mcp.opentrawl.sh"
	remoteMCPRelayDeviceKeyFileName    = "relay-device-key.hex"
	remoteMCPRelayTLSDirectoryName     = "relay-tls"
	remoteMCPRelayDomainHashContext    = "opentrawl-mcp-domain-v1|"
	remoteMCPRelayLoginSignContext     = "opentrawl-relay-login-v1|"
	remoteMCPRelayACMEContactEmail     = "tls@opentrawl.sh"
)

type remoteMCPRelayLaunchOptions struct {
	StateRoot             string
	RelayServerAddress    string
	RelayBaseDomain       string
	RelayTrustedCAFile    string
	ACMEDirectoryURL      string
	ACMETrustedRootsPEM   string
	DisableRelayClientTLS bool
}

type activeRemoteMCPRelayTunnel struct {
	PublicHostname string
	PublicOrigin   string
	stop           func()
}

func (tunnel *activeRemoteMCPRelayTunnel) Close() {
	if tunnel != nil && tunnel.stop != nil {
		tunnel.stop()
	}
}

func loadOrCreateRelayDeviceIdentity(stateRoot string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	keyPath := filepath.Join(stateRoot, agentAccessDirectoryName, remoteMCPRelayDeviceKeyFileName)
	encoded, err := os.ReadFile(keyPath)
	if err == nil {
		rawSeed, decodeErr := hex.DecodeString(strings.TrimSpace(string(encoded)))
		if decodeErr == nil && len(rawSeed) == ed25519.SeedSize {
			privateKey := ed25519.NewKeyFromSeed(rawSeed)
			publicKey := privateKey.Public().(ed25519.PublicKey)
			return publicKey, privateKey, nil
		}
		return nil, nil, errors.New("stored relay device key is corrupted: " + keyPath)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	seedHex := hex.EncodeToString(privateKey.Seed())
	if err := writePrivateStateFileAtomically(keyPath, []byte(seedHex+"\n")); err != nil {
		return nil, nil, err
	}
	return publicKey, privateKey, nil
}

// deriveRelayDeviceSubdomain deterministically binds an Ed25519 public key to a
// 20-character lowercase base32 subdomain (100 bits of entropy, DNS-safe) so
// the relay server needs zero database state.
func deriveRelayDeviceSubdomain(publicKey ed25519.PublicKey) string {
	digest := sha256.New()
	digest.Write([]byte(remoteMCPRelayDomainHashContext))
	digest.Write(publicKey)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest.Sum(nil))
	return strings.ToLower(encoded[:20])
}

func signRelayLoginMetadata(privateKey ed25519.PrivateKey, publicKey ed25519.PublicKey, now time.Time) map[string]string {
	pubHex := hex.EncodeToString(publicKey)
	timestampText := strconv.FormatInt(now.UTC().Unix(), 10)
	signature := ed25519.Sign(privateKey, []byte(remoteMCPRelayLoginSignContext+pubHex+"|"+timestampText))
	return map[string]string{
		"pubkey": pubHex,
		"ts":     timestampText,
		"sig":    hex.EncodeToString(signature),
	}
}

func startRemoteMCPRelayTunnel(
	ctx context.Context,
	options remoteMCPRelayLaunchOptions,
	handler http.Handler,
) (*activeRemoteMCPRelayTunnel, error) {
	serverHost, serverPortText, err := net.SplitHostPort(strings.TrimSpace(options.RelayServerAddress))
	if err != nil || serverHost == "" {
		return nil, errors.New("relay server address must be HOST:PORT, such as " + remoteMCPRelayDefaultServerAddress)
	}
	serverPort, err := strconv.Atoi(serverPortText)
	if err != nil || serverPort < 1 || serverPort > 65535 {
		return nil, errors.New("relay server port must be between 1 and 65535")
	}
	baseDomain := strings.TrimPrefix(strings.TrimSpace(options.RelayBaseDomain), ".")
	if baseDomain == "" {
		baseDomain = remoteMCPRelayDefaultBaseDomain
	}

	publicKey, privateKey, err := loadOrCreateRelayDeviceIdentity(options.StateRoot)
	if err != nil {
		return nil, err
	}
	deviceSubdomain := deriveRelayDeviceSubdomain(publicKey)
	deviceHostname := deviceSubdomain + "." + baseDomain

	rawLoopbackListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	localPort := rawLoopbackListener.Addr().(*net.TCPAddr).Port

	var trustedRoots *x509.CertPool
	if pemPath := strings.TrimSpace(options.ACMETrustedRootsPEM); pemPath != "" {
		pemBytes, readErr := os.ReadFile(pemPath)
		if readErr != nil {
			_ = rawLoopbackListener.Close()
			return nil, readErr
		}
		trustedRoots = x509.NewCertPool()
		if !trustedRoots.AppendCertsFromPEM(pemBytes) {
			_ = rawLoopbackListener.Close()
			return nil, errors.New("no valid PEM certificates in " + pemPath)
		}
	}

	tlsStorageDir := filepath.Join(options.StateRoot, agentAccessDirectoryName, remoteMCPRelayTLSDirectoryName)
	if err := os.MkdirAll(tlsStorageDir, 0o700); err != nil {
		_ = rawLoopbackListener.Close()
		return nil, err
	}

	var certConfig *certmagic.Config
	certCache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			return certConfig, nil
		},
		Logger: zap.NewNop(),
	})
	certConfig = certmagic.New(certCache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: tlsStorageDir},
		Logger:  zap.NewNop(),
	})

	acmeDirectory := strings.TrimSpace(options.ACMEDirectoryURL)
	if acmeDirectory == "" {
		acmeDirectory = certmagic.LetsEncryptProductionCA
	}
	primaryIssuer := certmagic.NewACMEIssuer(certConfig, certmagic.ACMEIssuer{
		CA:                      acmeDirectory,
		Email:                   remoteMCPRelayACMEContactEmail,
		Agreed:                  true,
		DisableHTTPChallenge:    true,
		DisableTLSALPNChallenge: false,
		ListenHost:              "127.0.0.1",
		AltTLSALPNPort:          localPort,
		TrustedRoots:            trustedRoots,
		Logger:                  zap.NewNop(),
	})
	issuers := []certmagic.Issuer{primaryIssuer}
	if strings.TrimSpace(options.ACMEDirectoryURL) == "" {
		issuers = append(issuers, certmagic.NewACMEIssuer(certConfig, certmagic.ACMEIssuer{
			CA:                      certmagic.ZeroSSLProductionCA,
			Email:                   remoteMCPRelayACMEContactEmail,
			Agreed:                  true,
			DisableHTTPChallenge:    true,
			DisableTLSALPNChallenge: false,
			ListenHost:              "127.0.0.1",
			AltTLSALPNPort:          localPort,
			Logger:                  zap.NewNop(),
		}))
	}
	certConfig.Issuers = issuers

	tlsConfig := certConfig.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)
	tlsListener := tls.NewListener(rawLoopbackListener, tlsConfig)

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          stdlog.New(io.Discard, "", 0),
	}
	go func() {
		_ = httpServer.Serve(tlsListener)
	}()

	// Silence frp's default stdout info logger so trawlkit/render owns stdout cleanly.
	frplog.InitLogger("console", "error", 0, true)

	// Connect stock embedded frpc over verified TLS to frps and claim the key-bound HTTPS SNI domain.
	loginFailExit := false
	tlsEnable := !options.DisableRelayClientTLS
	trustedCAFile := strings.TrimSpace(options.RelayTrustedCAFile)
	commonConfig := &v1.ClientCommonConfig{
		ServerAddr:    serverHost,
		ServerPort:    serverPort,
		LoginFailExit: &loginFailExit,
		Metadatas:     signRelayLoginMetadata(privateKey, publicKey, time.Now()),
		Log: v1.LogConfig{
			Level: "error",
		},
		Transport: v1.ClientTransportConfig{
			TLS: v1.TLSClientConfig{
				Enable: &tlsEnable,
				TLSConfig: v1.TLSConfig{
					UseSystemCa:   tlsEnable && trustedCAFile == "",
					TrustedCaFile: trustedCAFile,
					ServerName:    serverHost,
				},
			},
		},
	}
	if err := commonConfig.Complete(); err != nil {
		_ = httpServer.Close()
		certCache.Stop()
		return nil, err
	}
	proxyConfig := &v1.HTTPSProxyConfig{
		ProxyBaseConfig: v1.ProxyBaseConfig{
			Name: "opentrawl-" + deviceSubdomain,
			Type: "https",
			ProxyBackend: v1.ProxyBackend{
				LocalIP:   "127.0.0.1",
				LocalPort: localPort,
			},
		},
		DomainConfig: v1.DomainConfig{
			CustomDomains: []string{deviceHostname},
		},
	}
	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll([]v1.ProxyConfigurer{proxyConfig}, nil); err != nil {
		_ = httpServer.Close()
		certCache.Stop()
		return nil, err
	}
	frpService, err := client.NewService(client.ServiceOptions{
		Common:                 commonConfig,
		ConfigSourceAggregator: source.NewAggregator(configSource),
		ConnectorCreator: func(connCtx context.Context, cfg *v1.ClientCommonConfig) client.Connector {
			return &signingRelayConnector{
				inner:      client.NewConnector(connCtx, cfg),
				common:     cfg,
				privateKey: privateKey,
				publicKey:  publicKey,
			}
		},
	})
	if err != nil {
		_ = httpServer.Close()
		certCache.Stop()
		return nil, err
	}

	frpCtx, frpCancel := context.WithCancel(ctx)
	go func() {
		_ = frpService.Run(frpCtx)
	}()

	// Wait briefly for the tunnel proxy registration before triggering ACME TLS-ALPN-01.
	select {
	case <-time.After(600 * time.Millisecond):
	case <-ctx.Done():
		frpCancel()
		_ = httpServer.Close()
		certCache.Stop()
		return nil, ctx.Err()
	}

	if err := certConfig.ManageSync(ctx, []string{deviceHostname}); err != nil {
		frpCancel()
		_ = httpServer.Close()
		certCache.Stop()
		return nil, errors.New("TLS certificate setup over relay failed: " + err.Error())
	}

	return &activeRemoteMCPRelayTunnel{
		PublicHostname: deviceHostname,
		PublicOrigin:   "https://" + deviceHostname,
		stop: func() {
			frpCancel()
			_ = httpServer.Close()
			certCache.Stop()
		},
	}, nil
}

// resolveRelayDeviceOrigin derives this Mac's deterministic HTTPS origin before
// starting the HTTP server so fixedPublicOrigin is immutable once serving begins.
func resolveRelayDeviceOrigin(stateRoot string, relayBaseDomain string) (string, error) {
	baseDomain := strings.TrimPrefix(strings.TrimSpace(relayBaseDomain), ".")
	if baseDomain == "" {
		baseDomain = remoteMCPRelayDefaultBaseDomain
	}
	publicKey, _, err := loadOrCreateRelayDeviceIdentity(stateRoot)
	if err != nil {
		return "", err
	}
	return "https://" + deriveRelayDeviceSubdomain(publicKey) + "." + baseDomain, nil
}

// signingRelayConnector refreshes the Ed25519 login timestamp and signature on
// every control connection attempt so reconnects after 60 seconds succeed.
type signingRelayConnector struct {
	inner      client.Connector
	common     *v1.ClientCommonConfig
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

func (c *signingRelayConnector) Open() error {
	c.common.Metadatas = signRelayLoginMetadata(c.privateKey, c.publicKey, time.Now())
	return c.inner.Open()
}

func (c *signingRelayConnector) Connect() (net.Conn, error) {
	return c.inner.Connect()
}

func (c *signingRelayConnector) Close() error {
	return c.inner.Close()
}
