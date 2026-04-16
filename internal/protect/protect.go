package protect

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/pion/transport/v4"
)

// Protector is called with a socket file descriptor before connect.
// On Android, this calls VpnService.protect(fd) to bypass VPN routing.
var Protector func(fd int) bool

func controlFunc(network, address string, c syscall.RawConn) error {
	if Protector == nil {
		return nil
	}
	var err error
	c.Control(func(fd uintptr) {
		if !Protector(int(fd)) {
			err = &net.OpError{Op: "protect", Net: network, Err: net.ErrClosed}
		}
	})
	return err
}

// NewDialer returns a net.Dialer that calls Protector on each new socket.
func NewDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   controlFunc,
	}
}

// NewHTTPClient returns an http.Client using protected sockets.
func NewHTTPClient() *http.Client {
	dialer := NewDialer()
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:  10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// DialContext dials using a protected socket.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return NewDialer().DialContext(ctx, network, address)
}

// proxyDialer implements golang.org/x/net/proxy.Dialer for pion ICE.
type proxyDialer struct{}

func (d *proxyDialer) Dial(network, addr string) (net.Conn, error) {
	return NewDialer().Dial(network, addr)
}

// NewProxyDialer returns a proxy.Dialer that protects ICE sockets.
func NewProxyDialer() *proxyDialer {
	return &proxyDialer{}
}

// ProtectedNet wraps the standard net package with socket protection.
// This replaces the TCP-only proxy dialer approach, allowing UDP media
// (VP8 RTP) to work on Android while still protecting sockets via VpnService.protect().
type ProtectedNet struct{}

func NewProtectedNet() *ProtectedNet {
	return &ProtectedNet{}
}

func (n *ProtectedNet) ListenPacket(network, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: controlFunc}
	return lc.ListenPacket(context.Background(), network, address)
}

func (n *ProtectedNet) ListenUDP(network string, locAddr *net.UDPAddr) (transport.UDPConn, error) {
	addr := ""
	if locAddr != nil {
		addr = locAddr.String()
	}
	lc := net.ListenConfig{Control: controlFunc}
	pc, err := lc.ListenPacket(context.Background(), network, addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func (n *ProtectedNet) ListenTCP(network string, laddr *net.TCPAddr) (transport.TCPListener, error) {
	addr := ""
	if laddr != nil {
		addr = laddr.String()
	}
	lc := net.ListenConfig{Control: controlFunc}
	l, err := lc.Listen(context.Background(), network, addr)
	if err != nil {
		return nil, err
	}
	return &protectedTCPListener{l.(*net.TCPListener)}, nil
}

type protectedTCPListener struct {
	*net.TCPListener
}

func (l *protectedTCPListener) AcceptTCP() (transport.TCPConn, error) {
	conn, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (n *ProtectedNet) Dial(network, address string) (net.Conn, error) {
	return NewDialer().Dial(network, address)
}

func (n *ProtectedNet) DialUDP(network string, laddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	d := NewDialer()
	if laddr != nil {
		d.LocalAddr = laddr
	}
	conn, err := d.Dial(network, raddr.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}

func (n *ProtectedNet) DialTCP(network string, laddr, raddr *net.TCPAddr) (transport.TCPConn, error) {
	d := NewDialer()
	if laddr != nil {
		d.LocalAddr = laddr
	}
	conn, err := d.Dial(network, raddr.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func (n *ProtectedNet) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	return net.ResolveIPAddr(network, address)
}

func (n *ProtectedNet) ResolveUDPAddr(network, address string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr(network, address)
}

func (n *ProtectedNet) ResolveTCPAddr(network, address string) (*net.TCPAddr, error) {
	return net.ResolveTCPAddr(network, address)
}

func (n *ProtectedNet) Interfaces() ([]*transport.Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]*transport.Interface, len(ifs))
	for i, iface := range ifs {
		result[i] = transport.NewInterface(iface)
	}
	return result, nil
}

func (n *ProtectedNet) InterfaceByIndex(index int) (*transport.Interface, error) {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, err
	}
	return transport.NewInterface(*iface), nil
}

func (n *ProtectedNet) InterfaceByName(name string) (*transport.Interface, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	return transport.NewInterface(*iface), nil
}

func (n *ProtectedNet) CreateDialer(d *net.Dialer) transport.Dialer {
	if d == nil {
		d = NewDialer()
	} else {
		origControl := d.Control
		d.Control = func(network, address string, c syscall.RawConn) error {
			if err := controlFunc(network, address, c); err != nil {
				return err
			}
			if origControl != nil {
				return origControl(network, address, c)
			}
			return nil
		}
	}
	return d
}

func (n *ProtectedNet) CreateListenConfig(lc *net.ListenConfig) transport.ListenConfig {
	if lc == nil {
		lc = &net.ListenConfig{Control: controlFunc}
	} else {
		origControl := lc.Control
		lc.Control = func(network, address string, c syscall.RawConn) error {
			if err := controlFunc(network, address, c); err != nil {
				return err
			}
			if origControl != nil {
				return origControl(network, address, c)
			}
			return nil
		}
	}
	return lc
}

func (n *ProtectedNet) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	lc := net.ListenConfig{Control: controlFunc}
	return lc.Listen(ctx, network, address)
}
