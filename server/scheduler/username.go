// Package scheduler picks a device for each SOCKS5 request based on the
// username (user_id / country / sticky-duration / session) and live device
// stats (country, RTT, loss). It is intentionally decoupled from hub: the
// only inbound coupling is two small interfaces (DeviceLister, Pool) wired
// in main.go.
package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// UserToken is the parsed SOCKS5 username, e.g. "B_38313_US_5_Ab000001".
//
//	user_id   = "B_38313"   (everything before the country slot; may itself contain "_")
//	country   = "US"        (ISO-3166-1 alpha-2; "" or "000" means "any")
//	duration  = 5 minutes   (sticky window for IP usage; bounded by Min/MaxDuration)
//	session   = "Ab000001"  (8 chars; differentiates accounts under the same user_id)
type UserToken struct {
	UserID   string
	Country  string
	Duration time.Duration
	Session  string
}

// AuthKey is the credential lookup key. country/duration are scheduling
// parameters and don't affect identity; only (user_id, session) do.
func (t UserToken) AuthKey() string { return t.UserID + ":" + t.Session }

// UserKey is the sticky-binding key. Same value as AuthKey today, kept
// separate so we can change the binding granularity later without churning
// every call site (e.g., per-user-id stickiness for shared accounts).
func (t UserToken) UserKey() string { return t.AuthKey() }

// Bounds for the sticky window. The original spec says 5..90 minutes; we
// enforce here so a typo / hostile username can't pin a device for hours.
const (
	MinDuration = 5 * time.Minute
	MaxDuration = 90 * time.Minute
)

// SessionLen is the fixed length of the random session segment.
const SessionLen = 8

// ErrBadUsername is returned for any malformed username. Callers should treat
// it as an authentication failure (don't leak which field was bad).
var ErrBadUsername = errors.New("scheduler: malformed socks5 username")

// ParseUsername parses "user_id_<country>_<duration>_<session>".
//
// The user_id segment may itself contain underscores (e.g. "B_38313"); we
// peel the trailing 3 fixed-position segments off the right and treat
// whatever remains as the user_id. This keeps the parser stable as the
// user_id format evolves.
//
// Validation:
//   - At least 4 segments after split (user_id needs at least 1).
//   - duration is a positive integer of minutes, clamped to [MinDuration, MaxDuration].
//   - session is exactly SessionLen chars, alnum.
//   - country is "" / "000" (=any) or 2 ASCII letters.
func ParseUsername(s string) (UserToken, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return UserToken{}, ErrBadUsername
	}
	parts := strings.Split(s, "_")
	if len(parts) < 4 {
		return UserToken{}, ErrBadUsername
	}
	session := parts[len(parts)-1]
	durStr := parts[len(parts)-2]
	country := parts[len(parts)-3]
	userParts := parts[:len(parts)-3]
	if len(userParts) == 0 {
		return UserToken{}, ErrBadUsername
	}
	userID := strings.Join(userParts, "_")

	tok := UserToken{
		UserID:  userID,
		Session: session,
	}

	switch {
	case country == "" || country == "000":
		tok.Country = ""
	case len(country) == 2 && isASCIILetters(country):
		tok.Country = strings.ToUpper(country)
	default:
		return UserToken{}, fmt.Errorf("%w: bad country %q", ErrBadUsername, country)
	}

	mins, err := strconv.Atoi(durStr)
	if err != nil || mins <= 0 {
		return UserToken{}, fmt.Errorf("%w: bad duration %q", ErrBadUsername, durStr)
	}
	d := time.Duration(mins) * time.Minute
	if d < MinDuration {
		d = MinDuration
	}
	if d > MaxDuration {
		d = MaxDuration
	}
	tok.Duration = d

	if len(session) != SessionLen || !isAlnum(session) {
		return UserToken{}, fmt.Errorf("%w: bad session %q", ErrBadUsername, session)
	}

	if userID == "" {
		return UserToken{}, ErrBadUsername
	}
	return tok, nil
}

func isASCIILetters(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

func isAlnum(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
