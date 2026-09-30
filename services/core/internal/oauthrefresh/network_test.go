package oauthrefresh

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestRefreshDialChecksEveryDNSAddressAndPinsResolution(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []string
		trusted   bool
		dial      bool
	}{
		{"public", []string{"8.8.8.8"}, false, true},
		{"loopback", []string{"127.0.0.1"}, false, false},
		{"private", []string{"10.0.0.1"}, false, false},
		{"metadata", []string{"169.254.169.254"}, false, false},
		{"shared", []string{"100.100.100.200"}, false, false},
		{"mapped", []string{"::ffff:127.0.0.1"}, false, false},
		{"mixed", []string{"8.8.8.8", "10.0.0.1"}, false, false},
		{"ipv6-private", []string{"fd00::1"}, false, false},
		{"operator-trusted", []string{"10.0.0.1"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var origins []string
			if tc.trusted {
				origins = []string{"https://issuer.example:9443"}
			}
			p, err := newNetworkPolicy(origins)
			if err != nil {
				t.Fatal(err)
			}
			lookups, calls := 0, 0
			p.lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
				lookups++
				if host != "issuer.example" {
					t.Fatal(host)
				}
				var out []net.IPAddr
				for _, s := range tc.addresses {
					out = append(out, net.IPAddr{IP: net.ParseIP(s)})
				}
				return out, nil
			}
			p.dial = func(_ context.Context, _, address string) (net.Conn, error) {
				calls++
				if strings.Contains(address, "issuer.example") {
					t.Fatal("second DNS lookup allowed")
				}
				return nil, errors.New("test dial")
			}
			_, err = p.dialContext(context.Background(), "tcp", "issuer.example:9443")
			if err != ErrRefresh || lookups != 1 || (calls > 0) != tc.dial {
				t.Fatal("unexpected policy decision", lookups, calls, err)
			}
		})
	}
}

func TestOAuthTrustedOriginsAreExactAndOperatorOnly(t *testing.T) {
	for _, origin := range []string{"http://issuer.example", "https://user:secret@issuer.example", "https://issuer.example/token", "https://issuer.example?x=1", "https://issuer.example#fragment", ""} {
		if _, err := NewClient([]string{origin}); err == nil {
			t.Fatalf("accepted invalid trusted origin %q", origin)
		}
	}
	p, err := newNetworkPolicy([]string{"https://issuer.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.privateAddresses["issuer.example:443"] || p.privateAddresses["issuer.example:9443"] || p.privateAddresses["other.example:443"] {
		t.Fatal("trust expanded beyond exact origin")
	}
	for _, endpoint := range []string{"http://issuer.example/token", "https://x:y@issuer.example/token", "https://issuer.example/token#fragment"} {
		if _, err := parseEndpoint(endpoint); err == nil {
			t.Fatal("invalid endpoint")
		}
	}
}
