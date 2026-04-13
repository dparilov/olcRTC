// Package diagnostics provides lightweight SOCKS-routed tunnel checks for desktop and mobile shells.
package diagnostics

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// RunAll executes a concise set of SOCKS-routed checks and returns a user-facing text report.
func RunAll(ctx context.Context, socksHost string, socksPort int) string {
	results := []string{"== Tunnel diagnostics =="}

	results = append(results, formatResult("External IP", func() (string, error) {
		body, _, err := httpGET(ctx, socksHost, socksPort, "https://ifconfig.me")
		if err != nil {
			return "", err
		}

		return strings.TrimSpace(body), nil
	}))

	for _, target := range []string{
		"https://example.com",
		"https://cloudflare.com",
		"https://ifconfig.me/all.json",
	} {
		target := target
		results = append(results, formatResult(target, func() (string, error) {
			_, elapsed, err := httpGET(ctx, socksHost, socksPort, target)
			if err != nil {
				return "", err
			}

			return elapsed.String(), nil
		}))
	}

	results = append(results, formatResult("TCP 1.1.1.1:443", func() (string, error) {
		started := time.Now()
		if err := dialTCP(ctx, socksHost, socksPort, "1.1.1.1:443"); err != nil {
			return "", err
		}

		return time.Since(started).String(), nil
	}))

	return strings.Join(results, "\n")
}

func formatResult(label string, fn func() (string, error)) string {
	value, err := fn()
	if err != nil {
		return fmt.Sprintf("%s -> FAILED: %v", label, err)
	}

	return fmt.Sprintf("%s -> %s", label, value)
}

func httpGET(ctx context.Context, socksHost string, socksPort int, target string) (string, time.Duration, error) {
	client, err := newHTTPClient(ctx, socksHost, socksPort)
	if err != nil {
		return "", 0, err
	}

	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request %s: %w", target, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", 0, fmt.Errorf("read response: %w", err)
	}

	return string(body), time.Since(started), nil
}

func dialTCP(ctx context.Context, socksHost string, socksPort int, target string) error {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("%s:%d", socksHost, socksPort), nil, &net.Dialer{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create SOCKS dialer: %w", err)
	}

	type contextDialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}

	if withContext, ok := dialer.(contextDialer); ok {
		conn, err := withContext.DialContext(ctx, "tcp", target)
		if err != nil {
			return fmt.Errorf("dial %s: %w", target, err)
		}
		_ = conn.Close()
		return nil
	}

	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}
	_ = conn.Close()
	return nil
}

func newHTTPClient(ctx context.Context, socksHost string, socksPort int) (*http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("%s:%d", socksHost, socksPort), nil, &net.Dialer{
		Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create SOCKS dialer: %w", err)
	}

	transport := &http.Transport{}

	type contextDialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}

	if withContext, ok := dialer.(contextDialer); ok {
		transport.DialContext = withContext.DialContext
	} else {
		transport.DialContext = func(_ context.Context, network string, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	}

	_ = ctx

	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}, nil
}
