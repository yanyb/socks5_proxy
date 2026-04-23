module xsocks5

go 1.25.0

require (
	github.com/hashicorp/yamux v0.1.2
	github.com/nsqio/go-nsq v1.1.0
	github.com/oschwald/geoip2-golang v1.13.0
	github.com/sirupsen/logrus v1.9.4
	github.com/things-go/go-socks5 v0.1.1
	go.mongodb.org/mongo-driver/v2 v2.5.1
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/golang/snappy v0.0.1 // indirect
	github.com/klauspost/compress v1.17.6 // indirect
	github.com/oschwald/maxminddb-golang v1.13.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)

// Local forks for project-specific changes (CLOSE_WAIT / relay semantics, yamux tuning, etc.).
replace github.com/things-go/go-socks5 => ./third_party/go-socks5

replace github.com/hashicorp/yamux => ./third_party/yamux
