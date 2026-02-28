module tg-podcastotron

go 1.25.0

require (
	github.com/XSAM/otelsql v0.41.0
	github.com/aws/aws-sdk-go-v2 v1.41.2
	github.com/aws/aws-sdk-go-v2/config v1.32.10
	github.com/aws/aws-sdk-go-v2/credentials v1.19.10
	github.com/aws/aws-sdk-go-v2/service/s3 v1.96.1
	github.com/dir01/sqlq v0.0.0-20250504200031-1a45b2d4dc71
	github.com/go-telegram/bot v1.19.0
	github.com/google/uuid v1.6.0
	github.com/jbub/podcasts v0.2.0
	github.com/jmoiron/sqlx v1.4.0
	github.com/joho/godotenv v1.5.1
	github.com/mattn/go-sqlite3 v1.14.34
	github.com/rubenv/sql-migrate v1.8.1
	go.opentelemetry.io/contrib/bridges/otelslog v0.15.0
	go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws v0.65.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.40.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.40.0
	go.opentelemetry.io/otel/metric v1.40.0
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel/sdk/metric v1.40.0
	go.opentelemetry.io/otel/trace v1.40.0
	go.uber.org/multierr v1.11.0
	golang.org/x/exp v0.0.0-20260218203240-3dfff04db8fa
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.3.1 // indirect
	github.com/Masterminds/sprig/v3 v3.2.3 // indirect
	github.com/armon/go-radix v1.0.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.5 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.18 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.18 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.18 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.18 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.54.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.18 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.18 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.39.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.42.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.7 // indirect
	github.com/aws/smithy-go v1.24.1 // indirect
	github.com/bgentry/speakeasy v0.1.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/denisenkom/go-mssqldb v0.9.0 // indirect
	github.com/docker/docker v28.5.2+incompatible // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/go-gorp/gorp/v3 v3.1.0 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-sql-driver/mysql v1.8.1 // indirect
	github.com/godror/godror v0.40.4 // indirect
	github.com/godror/knownpb v0.1.1 // indirect
	github.com/golang-sql/civil v0.0.0-20190719163853-cb61b32ac6fe // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.7 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/huandu/xstrings v1.4.0 // indirect
	github.com/imdario/mergo v0.3.13 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/lufia/plan9stats v0.0.0-20260216142805-b3301c5f2a88 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-oci8 v0.1.1 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mitchellh/cli v1.1.5 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/go-archive v0.2.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/morikuni/aec v1.1.0 // indirect
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/posener/complete v1.2.3 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/shirou/gopsutil/v4 v4.26.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/testcontainers/testcontainers-go v0.40.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.65.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.40.0 // indirect
	go.opentelemetry.io/otel/log v0.16.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260128011058-8636f8732409 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260128011058-8636f8732409 // indirect
	google.golang.org/grpc v1.78.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

tool github.com/rubenv/sql-migrate/sql-migrate
