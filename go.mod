module uptime-canary

go 1.27.4

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dlclark/regexp2/v2 v2.13.2 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-sourcemap/sourcemap v2.1.4+incompatible // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/google/pprof v0.0.0-20261118010117-084b33a85f63 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/klauspost/compress v1.20.5 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.34 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/shopspring/decimal v1.6.0 // indirect
	github.com/xdg-go/stringprep v1.0.5 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261118221215-c8830f628b3f // indirect
	modernc.org/libc v1.82.9 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.13.0 // indirect
)

require (
	github.com/dop251/goja v0.0.0-20261121162556-79d6ad37b976
	github.com/go-sql-driver/mysql v1.10.2
	github.com/gosnmp/gosnmp v1.47.0
	github.com/jackc/pgx/v5 v5.13.0
	github.com/microsoft/go-mssqldb v1.12.0
	github.com/pkg/sftp v1.13.12
	github.com/sijms/go-ora/v3 v3.0.1
	github.com/twmb/franz-go v1.22.3
	github.com/twmb/franz-go/pkg/kmsg v1.15.1
	github.com/xdg-go/scram v1.4.0
	go.mongodb.org/mongo-driver/v2 v2.10.1
	golang.org/x/crypto v0.59.0
	golang.org/x/net v0.63.0
	golang.org/x/sys v0.50.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
	modernc.org/sqlite v1.62.0
)

replace github.com/dop251/goja => ./apps/node-go/third_party/goja
