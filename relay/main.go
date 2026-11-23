package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	plugin "github.com/fatedier/frp/pkg/plugin/server"
	"github.com/fatedier/frp/server"
)

const (
	relayDomainHashContext = "opentrawl-mcp-domain-v1|"
	relayLoginSignContext  = "opentrawl-relay-login-v1|"
	relayLoginSkewSeconds  = 60
)

type relayPluginRequestEnvelope struct {
	Version string          `json:"version"`
	Op      string          `json:"op"`
	Content json.RawMessage `json:"content"`
}

func deriveDeviceSubdomain(publicKey ed25519.PublicKey) string {
	digest := sha256.New()
	digest.Write([]byte(relayDomainHashContext))
	digest.Write(publicKey)
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest.Sum(nil))
	return strings.ToLower(encoded[:20])
}

func startStatelessEd25519AuthPlugin(baseDomain string) (string, func(), error) {
	suffix := "." + strings.TrimPrefix(strings.TrimSpace(baseDomain), ".")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/frp-plugin", func(response http.ResponseWriter, request *http.Request) {
		var envelope relayPluginRequestEnvelope
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			http.Error(response, "invalid plugin request", http.StatusBadRequest)
			return
		}
		pluginResponse := evaluateRelayPluginOperation(envelope, suffix, time.Now())
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(pluginResponse)
	})
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return listener.Addr().String(), func() { _ = httpServer.Close() }, nil
}

func evaluateRelayPluginOperation(envelope relayPluginRequestEnvelope, domainSuffix string, now time.Time) plugin.Response {
	switch envelope.Op {
	case "Login":
		var content plugin.LoginContent
		if err := json.Unmarshal(envelope.Content, &content); err != nil {
			return plugin.Response{Reject: true, RejectReason: "invalid login payload"}
		}
		pubHex := content.Metas["pubkey"]
		tsText := content.Metas["ts"]
		sigHex := content.Metas["sig"]
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
		pubBytes, err := hex.DecodeString(content.User.Metas["pubkey"])
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			return plugin.Response{Reject: true, RejectReason: "verified pubkey missing"}
		}
		expectedDomain := deriveDeviceSubdomain(ed25519.PublicKey(pubBytes)) + domainSuffix
		if content.CustomDomains[0] != expectedDomain {
			return plugin.Response{Reject: true, RejectReason: "requested domain does not match device key"}
		}
		return plugin.Response{Unchange: true}

	default:
		return plugin.Response{Reject: true, RejectReason: "unsupported plugin operation"}
	}
}

func runRelayServer(ctx context.Context, bindAddr string, bindPort, httpsPort int, baseDomain string) error {
	pluginAddr, stopPlugin, err := startStatelessEd25519AuthPlugin(baseDomain)
	if err != nil {
		return err
	}
	defer stopPlugin()

	serverConfig := &v1.ServerConfig{
		BindAddr:       bindAddr,
		BindPort:       bindPort,
		VhostHTTPSPort: httpsPort,
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

func main() {
	bindAddr := flag.String("bind-addr", "0.0.0.0", "Address for frps control and HTTPS SNI passthrough")
	bindPort := flag.Int("bind-port", 7000, "TCP port for frpc control plane")
	httpsPort := flag.Int("https-port", 443, "TCP port for raw TLS SNI passthrough")
	baseDomain := flag.String("domain", "mcp.opentrawl.sh", "Base domain for key-bound subdomains")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runRelayServer(ctx, *bindAddr, *bindPort, *httpsPort, *baseDomain); err != nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}
