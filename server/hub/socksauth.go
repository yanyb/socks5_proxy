package hub

import (
	"strings"

	"github.com/things-go/go-socks5"
)

// SOCKS5Username returns the RFC SOCKS5 username from the request auth context, or "".
func SOCKS5Username(req *socks5.Request) string {
	if req == nil || req.AuthContext == nil || req.AuthContext.Payload == nil {
		return ""
	}
	return strings.TrimSpace(req.AuthContext.Payload["username"])
}

// SOCKSPlainAuth implements go-socks5 CredentialStore: password is a shared secret;
// the username must be non-empty and is interpreted as device_id by the server.
type SOCKSPlainAuth struct {
	Password string
}

// Valid returns true when password matches and user (device_id) is non-empty.
func (s *SOCKSPlainAuth) Valid(user, password, userAddr string) bool {
	if s == nil || s.Password == "" {
		return false
	}
	return password == s.Password && strings.TrimSpace(user) != ""
}
