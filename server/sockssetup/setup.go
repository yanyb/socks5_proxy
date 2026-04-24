// Package sockssetup wires SOCKS5 server options from config and hub (credentials, IP whitelist).
package sockssetup

import (
	"context"
	"net/http"
	"strings"
	"time"

	"xsocks5/server/config"
	"xsocks5/server/hub"
	"xsocks5/server/scheduler"

	"github.com/sirupsen/logrus"
	"github.com/things-go/go-socks5"
)

// SchedulerEnabled reports whether the config has opted into the scheduler-
// based username scheme. main.go uses this to decide whether to instantiate
// the scheduler at all (and to gate the dial-path through it).
func SchedulerEnabled(cfg *config.Server) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.SocksUsernameScheme), "scheduler")
}

// BuildSocksRuleSet returns a socks5 RuleSet. When socks_ip_whitelist_url or
// socks_ip_whitelist_file is set, a cached IP allowlist (default refresh 1m) is
// applied before the usual command permit (CONNECT only). If both are set, URL
// is used. No IP key in config => no source => base PermitCommand only.
func BuildSocksRuleSet(ctx context.Context, cfg *config.Server, log *logrus.Logger) (socks5.RuleSet, error) {
	inner := socks5.RuleSet(&socks5.PermitCommand{
		EnableConnect: true, EnableBind: false, EnableAssociate: false,
	})
	refresh := cfg.SocksIPWhitelistRefresh

	if u := strings.TrimSpace(cfg.SocksIPWhitelistURL); u != "" {
		src := &hub.URLJSONIPWhitelistSource{URL: u, Client: &http.Client{Timeout: 30 * time.Second}}
		c := hub.NewIPWhitelistCache(src, refresh, log)
		if err := c.Start(ctx); err != nil {
			return nil, err
		}
		return hub.NewIPRuleSet(c, inner, log), nil
	}
	if p := strings.TrimSpace(cfg.SocksIPWhitelistFile); p != "" {
		src := &hub.JSONFileIPWhitelistSource{Path: p}
		c := hub.NewIPWhitelistCache(src, refresh, log)
		if err := c.Start(ctx); err != nil {
			return nil, err
		}
		return hub.NewIPRuleSet(c, inner, log), nil
	}
	return inner, nil
}

// SocksIPWhitelistSummary is a one-line description for the boot log.
func SocksIPWhitelistSummary(cfg *config.Server) string {
	if u := strings.TrimSpace(cfg.SocksIPWhitelistURL); u != "" {
		return "url (refresh " + cfg.SocksIPWhitelistRefresh.String() + "): " + u
	}
	if p := strings.TrimSpace(cfg.SocksIPWhitelistFile); p != "" {
		return "file (refresh " + cfg.SocksIPWhitelistRefresh.String() + "): " + p
	}
	return "off"
}

// ConfigureSocksAuth wires the SOCKS5 credential store. Precedence:
//  1. socks_credentials_file -> file-backed CredentialCache (eager preload + 1m refresh).
//  2. socks_auth_password    -> single shared secret (legacy).
//  3. neither                -> no auth (only valid when exactly one device is online).
//
// When socks_username_scheme = "scheduler", the credential cache key is the
// (user_id, session) auth key, not the raw SOCKS5 username. We wrap with
// scheduler.UsernameCredentialStore so the scheduler-style usernames work
// against a credentials file that lists one entry per account (not per
// (account x country x duration) combination).
//
// Returns a short human description for the boot banner.
func ConfigureSocksAuth(ctx context.Context, cfg *config.Server, log *logrus.Logger, opts *[]socks5.Option) (string, error) {
	useScheduler := SchedulerEnabled(cfg)

	if path := strings.TrimSpace(cfg.SocksCredentialsFile); path != "" {
		src := &hub.JSONFileCredentialSource{Path: path}
		cache := hub.NewCredentialCache(src, cfg.SocksCredentialsRefresh, log)
		if err := cache.Start(ctx); err != nil {
			return "", err
		}
		if useScheduler {
			*opts = append(*opts, socks5.WithCredential(&scheduler.UsernameCredentialStore{Inner: cache}))
			return "user/pass (cache from " + path + ", scheduler scheme; key=user_id:session)", nil
		}
		*opts = append(*opts, socks5.WithCredential(cache))
		return "user/pass (cache from " + path + ", refresh " + cfg.SocksCredentialsRefresh.String() + ")", nil
	}
	if cfg.SocksAuthPassword != "" {
		// In scheduler scheme the username is parsed and the user_id:session
		// is what would normally be checked against the password store. With
		// only a single shared secret available, we still parse to enforce
		// the username format (otherwise any string would auth). We accept
		// any well-formed username + the shared secret.
		if useScheduler {
			*opts = append(*opts, socks5.WithCredential(&scheduler.UsernameCredentialStore{
				Inner: &hub.SOCKSPlainAuth{Password: cfg.SocksAuthPassword},
			}))
			return "user/pass (shared secret, scheduler scheme username)", nil
		}
		*opts = append(*opts, socks5.WithCredential(&hub.SOCKSPlainAuth{Password: cfg.SocksAuthPassword}))
		return "user/pass (shared secret, username = device_id)", nil
	}
	return "noauth (only valid when exactly one device is online)", nil
}
