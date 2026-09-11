// Package netguard keeps operator configured outbound calls from reaching
// addresses the deployment did not intend. Offline installations legitimately
// call internal hosts, so the policy is a decision the caller passes in rather
// than a fixed rule.
package netguard

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"time"
)

func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// 100.64.0.0/10 carrier grade NAT and 169.254.169.254 style metadata ranges
	// are neither private nor routable for this purpose.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

func IsPrivateHost(host string) bool {
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return IsPrivateIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return true
	}
	for _, address := range addresses {
		if IsPrivateIP(address.IP) {
			return true
		}
	}
	return false
}

// Client builds an HTTP client whose dialer rejects private destinations when
// the deployment policy forbids them, closing the DNS rebinding gap between
// validation time and delivery time.
func Client(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if IsPrivateIP(net.ParseIP(host)) {
				return &net.AddrError{Err: "내부망 주소로는 전달할 수 없습니다", Addr: host}
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			MaxIdleConns:          32,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
