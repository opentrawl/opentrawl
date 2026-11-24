package pluginproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DialTCPThroughHTTPClient opens a raw TCP stream through the same authenticated
// CONNECT proxy as the plugin's HTTP requests.
func DialTCPThroughHTTPClient(ctx context.Context, client *http.Client, network, address string) (net.Conn, error) {
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		return nil, errors.New("plugin TCP connection requires a CONNECT proxy")
	}
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Scheme: "https", Host: address},
		Host:   address,
		Header: make(http.Header),
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		return nil, err
	}
	if proxyURL == nil || proxyURL.Scheme != "http" || proxyURL.User == nil {
		return nil, errors.New("plugin TCP connection requires an authenticated HTTP CONNECT proxy")
	}
	password, _ := proxyURL.User.Password()
	request.SetBasicAuth(proxyURL.User.Username(), password)
	request.Header.Set("Proxy-Authorization", request.Header.Get("Authorization"))
	request.Header.Del("Authorization")

	ctx, cancel := context.WithTimeout(ctx, proxyDialTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, network, proxyURL.Host)
	if err != nil {
		return nil, err
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	reader, err := openTCPTunnel(connection, request)
	if err != nil {
		stopCancellation()
		_ = connection.Close()
		return nil, err
	}
	if !stopCancellation() {
		_ = connection.Close()
		return nil, ctx.Err()
	}
	return &bufferedProxyConnection{Conn: connection, reader: reader}, nil
}

func openTCPTunnel(connection net.Conn, request *http.Request) (*bufio.Reader, error) {
	if err := connection.SetDeadline(time.Now().Add(proxyDialTimeout)); err != nil {
		return nil, err
	}
	if err := request.Write(connection); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("plugin CONNECT to %s failed with HTTP %d", request.Host, response.StatusCode)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return reader, nil
}

type bufferedProxyConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (connection *bufferedProxyConnection) Read(buffer []byte) (int, error) {
	return connection.reader.Read(buffer)
}
