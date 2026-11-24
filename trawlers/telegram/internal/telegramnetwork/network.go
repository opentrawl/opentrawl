// Package telegramnetwork routes Telegram's MTProto connections through the plugin broker.
package telegramnetwork

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"

	"github.com/gotd/td/telegram/dcs"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
)

func NetworkHosts() []string {
	// Telegram for macOS also uses these endpoints when restoring a Postbox session.
	hosts := []string{"149.154.167.51:443", "91.108.56.130:443"}
	for _, endpoint := range dcs.Prod().Options {
		hosts = append(hosts, net.JoinHostPort(endpoint.IPAddress, strconv.Itoa(endpoint.Port)))
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

func Resolver(httpClient *http.Client) dcs.Resolver {
	if httpClient == nil {
		return dcs.DefaultResolver()
	}
	return dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return pluginproxy.DialTCPThroughHTTPClient(ctx, httpClient, network, address)
	}})
}
