package hub

import (
	"context"
	"net"
	"testing"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

func TestHostIPFromAddr(t *testing.T) {
	cases := []struct {
		addr    net.Addr
		want    string
		wantNil bool
	}{
		{&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234}, "10.0.0.1", false},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 1080}, "::1", false},
		{&net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.1"), Port: 0}, "192.0.2.1", false},
	}
	for _, tc := range cases {
		ip := HostIPFromAddr(tc.addr)
		if tc.wantNil {
			if ip != nil {
				t.Errorf("%v: want nil, got %v", tc.addr, ip)
			}
			continue
		}
		if ip == nil || ip.String() != tc.want {
			t.Errorf("%v: want %q, got %v", tc.addr, tc.want, ip)
		}
	}
}

func TestIPWhitelistCache_Allows(t *testing.T) {
	ctx := context.Background()
	src := &StaticCIDRsSource{CIDRs: []string{"10.0.0.0/8", "2001:db8::/32"}}
	c := NewIPWhitelistCache(src, 0, nil)
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.Allows(net.ParseIP("10.1.2.3")) {
		t.Fatal("10.1.2.3 should be allowed")
	}
	if c.Allows(net.ParseIP("8.8.8.8")) {
		t.Fatal("8.8.8.8 should be denied")
	}
	if c.Count() < 1 {
		t.Fatalf("expected parsed nets, count=%d", c.Count())
	}
}

func TestIPRuleSet_Allow(t *testing.T) {
	ctx := context.Background()
	src := &StaticCIDRsSource{CIDRs: []string{"127.0.0.0/8"}}
	whitelist := NewIPWhitelistCache(src, 0, nil)
	if err := whitelist.Start(ctx); err != nil {
		t.Fatal(err)
	}
	inner := &socks5.PermitCommand{EnableConnect: true, EnableBind: false, EnableAssociate: false}
	r := NewIPRuleSet(whitelist, inner, nil)
	req := &socks5.Request{
		Request: statute.Request{
			Command: statute.CommandConnect,
		},
		RemoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9999},
	}
	_, ok := r.Allow(ctx, req)
	if !ok {
		t.Fatal("local connect should be allowed")
	}
	req.RemoteAddr = &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 1}
	_, ok = r.Allow(ctx, req)
	if ok {
		t.Fatal("8.8.8.8 should be denied by whitelist before inner")
	}
}

func TestParseCIDRStringList(t *testing.T) {
	_, err := parseCIDRStringList([]string{"not-an-ip"})
	if err == nil {
		t.Fatal("expected error for invalid line")
	}
	nets, err := parseCIDRStringList([]string{"192.0.2.1", "0.0.0.0/0"})
	if err != nil || len(nets) < 2 {
		t.Fatalf("err=%v, nets=%d", err, len(nets))
	}
}
