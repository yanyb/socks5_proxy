module xsocks5

go 1.25.0

require (
	github.com/hashicorp/yamux v0.1.2
	github.com/sirupsen/logrus v1.9.4
	github.com/things-go/go-socks5 v0.1.1
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
)

// Local forks for project-specific changes (CLOSE_WAIT / relay semantics, yamux tuning, etc.).
replace github.com/things-go/go-socks5 => ./third_party/go-socks5

replace github.com/hashicorp/yamux => ./third_party/yamux
