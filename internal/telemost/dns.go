package telemost

import (
	"context"
	"log"
	"net"
	"sync"
)

var (
	dnsOnce   sync.Once
	dnsServer string
)

// SetDNSServer configures a custom DNS resolver for Telemost API requests.
// addr should be in "host:port" format, e.g. "1.1.1.1:53".
// Only the first call takes effect (subsequent calls are no-ops).
func SetDNSServer(addr string) {
	dnsOnce.Do(func() {
		dnsServer = addr
		log.Printf("[DNS] custom resolver: %s", addr)
		net.DefaultResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, "udp", addr)
			},
		}
	})
}
