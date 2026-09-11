package netguard

import (
	"net"
	"testing"
)

func TestIsPrivateIPCoversInternalRanges(t *testing.T) {
	private := []string{"127.0.0.1", "10.1.2.3", "192.168.0.5", "172.16.4.4", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "0.0.0.0"}
	for _, value := range private {
		if !IsPrivateIP(net.ParseIP(value)) {
			t.Fatalf("%s must be treated as private", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "203.0.113.10", "2001:4860:4860::8888"} {
		if IsPrivateIP(net.ParseIP(value)) {
			t.Fatalf("%s must be treated as public", value)
		}
	}
	if !IsPrivateIP(nil) {
		t.Fatal("an unresolvable address must fail closed")
	}
}

func TestIsPrivateHostAcceptsLiteralAddresses(t *testing.T) {
	if !IsPrivateHost("10.0.0.9") {
		t.Fatal("private literal must be detected without DNS")
	}
	if IsPrivateHost("8.8.4.4") {
		t.Fatal("public literal must not be flagged")
	}
	if !IsPrivateHost("") {
		t.Fatal("an empty host must fail closed")
	}
}
