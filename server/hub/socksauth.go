package hub

import "strings"

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
