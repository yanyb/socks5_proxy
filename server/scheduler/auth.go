package scheduler

import "strings"

// AuthLookup is the small surface the scheduler needs from any credential
// store. *hub.CredentialCache satisfies it (and so does any future admin-API
// backed store). Defined here so scheduler stays independent of hub.
type AuthLookup interface {
	Valid(user, password, userAddr string) bool
}

// UsernameCredentialStore is a socks5.CredentialStore that:
//
//  1. Accepts the new 5-segment username (B_<id>_<country>_<dur>_<sess>).
//  2. Computes the auth key = "<user_id>:<session>" -- country/duration are
//     scheduling parameters and are ignored for auth.
//  3. Delegates the password check to the inner AuthLookup using that key.
//
// Effect: ops can rotate country/duration in the username without touching
// the credentials table; one entry covers every scheduling combo of a given
// (user_id, session) account.
type UsernameCredentialStore struct {
	Inner AuthLookup
}

// Valid implements socks5.CredentialStore.
//
// Returns false on any parse error -- we treat malformed usernames as auth
// failures rather than leaking which field was bad.
func (u *UsernameCredentialStore) Valid(user, password, userAddr string) bool {
	if u == nil || u.Inner == nil {
		return false
	}
	tok, err := ParseUsername(user)
	if err != nil {
		return false
	}
	if strings.TrimSpace(password) == "" {
		return false
	}
	return u.Inner.Valid(tok.AuthKey(), password, userAddr)
}
