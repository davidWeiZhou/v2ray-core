package websocket

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"v2ray.com/core/common"
	"v2ray.com/core/common/net"
	http_proto "v2ray.com/core/common/protocol/http"
	"v2ray.com/core/common/session"
	"v2ray.com/core/transport/internet"
	v2tls "v2ray.com/core/transport/internet/tls"
)

type requestHandler struct {
	path string
	ln   *Listener
}

var upgrader = &websocket.Upgrader{
	ReadBufferSize:   4 * 1024,
	WriteBufferSize:  4 * 1024,
	HandshakeTimeout: time.Second * 8,
}

func (h *requestHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != h.path {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		newError("failed to convert to WebSocket connection").Base(err).WriteToLog()
		return
	}

	forwardedAddrs := http_proto.ParseXForwardedFor(request.Header)
	remoteAddr := conn.RemoteAddr()
	if len(forwardedAddrs) > 0 && forwardedAddrs[0].Family().Either(net.AddressFamilyIPv4, net.AddressFamilyIPv6) {
		remoteAddr.(*net.TCPAddr).IP = forwardedAddrs[0].IP()
	}

	h.ln.addConn(newConnection(conn, remoteAddr))
}

type Listener struct {
	sync.Mutex
	listener net.Listener
	config   *Config
	addConn  internet.ConnHandler
}

func ListenWS(ctx context.Context, address net.Address, port net.Port, addConn internet.ConnHandler) (internet.Listener, error) {
	networkSettings := internet.StreamSettingsFromContext(ctx)
	wsSettings := networkSettings.ProtocolSettings.(*Config)

	var tlsConfig *tls.Config
	if config := v2tls.ConfigFromContext(ctx); config != nil {
		tlsConfig = config.GetTLSConfig()
	}

	listener, err := listenTCP(ctx, address, port, tlsConfig)
	if err != nil {
		return nil, err
	}

	l := &Listener{
		config:   wsSettings,
		addConn:  addConn,
		listener: listener,
	}

	go func() {
		if err := l.serve(); err != nil {
			newError("failed to serve http for WebSocket").Base(err).AtWarning().WriteToLog(session.ExportIDToError(ctx))
		}
	}()

	return l, err
}

func listenTCP(ctx context.Context, address net.Address, port net.Port, tlsConfig *tls.Config) (net.Listener, error) {
	listener, err := internet.ListenSystem(ctx, &net.TCPAddr{
		IP:   address.IP(),
		Port: int(port),
	})
	if err != nil {
		return nil, newError("failed to listen TCP on", address, ":", port).Base(err)
	}

	if tlsConfig != nil {
		return tls.NewListener(listener, tlsConfig), nil
	}

	return listener, nil
}

func (ln *Listener) serve() error {
	return http.Serve(ln.listener, &requestHandler{
		path: ln.config.GetNormalizedPath(),
		ln:   ln,
	})
}

// Addr implements net.Listener.Addr().
func (ln *Listener) Addr() net.Addr {
	return ln.listener.Addr()
}

// Close implements net.Listener.Close().
func (ln *Listener) Close() error {
	return ln.listener.Close()
}

func init() {
	common.Must(internet.RegisterTransportListener(protocolName, ListenWS))
}
