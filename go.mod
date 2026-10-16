module bfiber

go 1.27.2

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.1.0
	charm.land/huh/v2 v2.0.3
	charm.land/lipgloss/v2 v2.0.6
	github.com/InfluxCommunity/influxdb3-go/v2 v2.17.0
	github.com/ThreeDotsLabs/watermill v1.5.3
	github.com/aarondl/opt v0.0.0-20250607033636-982744e1bd65
	github.com/adhocore/gronx v1.20.5
	github.com/alexliesenfeld/health v0.8.1
	github.com/apache/arrow-go/v18 v18.8.0
	github.com/bits-and-blooms/bloom/v3 v3.7.1
	github.com/brianvoe/gofakeit/v7 v7.18.0
	github.com/caarlos0/env/v11 v11.4.1
	github.com/casbin/casbin/v3 v3.11.0
	github.com/centrifugal/gocent/v3 v3.4.0
	github.com/charmbracelet/x/term v0.2.2
	github.com/dromara/carbon/v2 v2.6.18
	github.com/dustin/go-humanize v1.1.0
	github.com/gabriel-vasile/mimetype v1.4.15
	github.com/getsentry/sentry-go v0.50.0
	github.com/go-pay/gopay v1.5.123
	github.com/go-playground/validator/v10 v10.30.5
	github.com/go-resty/resty/v2 v2.17.2
	github.com/gofiber/contrib/v3/casbin/v2 v2.0.6
	github.com/gofiber/contrib/v3/circuitbreaker v1.1.6
	github.com/gofiber/contrib/v3/i18n v1.0.13
	github.com/gofiber/contrib/v3/jwt v1.2.6
	github.com/gofiber/contrib/v3/otel v1.3.1
	github.com/gofiber/contrib/v3/sentry v1.1.13
	github.com/gofiber/contrib/v3/zap v1.0.14
	github.com/gofiber/fiber/v3 v3.6.1
	github.com/gofiber/storage/memory/v2 v2.1.2
	github.com/gofiber/storage/rueidis v1.5.2
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/hibiken/asynq v0.26.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/johnfercher/maroto/v2 v2.4.3
	github.com/joho/godotenv v1.5.1
	github.com/lionsoul2014/ip2region/binding/golang v0.0.0-20261014021221-1bf6cabfb63c
	github.com/meilisearch/meilisearch-go v0.36.3
	github.com/minio/minio-go/v7 v7.4.1
	github.com/nicksnyder/go-i18n/v2 v2.6.1
	github.com/paulmach/orb v0.13.0
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/pgvector/pgvector-go v0.4.1
	github.com/redis/go-redis/v9 v9.23.0
	github.com/redis/rueidis v1.0.79
	github.com/resend/resend-go/v2 v2.28.0
	github.com/samber/hot v0.13.1
	github.com/samber/lo v1.53.0
	github.com/samber/oops v1.23.2
	github.com/shopspring/decimal v1.5.0
	github.com/spf13/cast v1.10.0
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.1
	github.com/uptrace/bun v1.3.0
	github.com/uptrace/bun/dialect/pgdialect v1.3.0
	github.com/uptrace/bun/extra/bunotel v1.3.0
	github.com/valyala/fasthttp v1.75.0
	github.com/wneessen/go-mail v0.8.1
	github.com/xuri/excelize/v2 v2.11.0
	github.com/yeqown/go-qrcode/v2 v2.3.0
	github.com/yeqown/go-qrcode/writer/standard v1.4.0
	go.opentelemetry.io/contrib/bridges/otelzap v0.21.0
	go.opentelemetry.io/contrib/instrumentation/runtime v0.72.0
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.23.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.47.0
	go.opentelemetry.io/otel/metric v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/log v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
	go.temporal.io/sdk v1.50.0
	go.temporal.io/sdk/contrib/opentelemetry v0.8.1
	go.uber.org/goleak v1.3.0
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.47.0
	golang.org/x/net v0.61.0
	golang.org/x/sync v0.24.0
	golang.org/x/text v0.43.0
)

require (
	github.com/DmitriyVTitov/size v1.5.0 // indirect
	github.com/MicahParks/keyfunc/v2 v2.1.0 // indirect
	github.com/andybalholm/brotli v1.2.3 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.24.6 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.3 // indirect
	github.com/boombuler/barcode v1.1.0 // indirect
	github.com/casbin/govaluate v1.10.0 // indirect
	github.com/catppuccin/go v0.2.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/harmonica v0.2.0 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4 // indirect
	github.com/charmbracelet/x/ansi v0.11.9 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/exp/strings v0.0.0-20240722160745-212f7b056ed0 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/fogleman/gg v1.3.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.6 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-pay/crypto v0.0.3 // indirect
	github.com/go-pay/errgroup v0.0.3 // indirect
	github.com/go-pay/smap v0.0.2 // indirect
	github.com/go-pay/util v0.0.4 // indirect
	github.com/go-pay/xlog v0.0.3 // indirect
	github.com/go-pay/xtime v0.0.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/gofiber/schema v1.8.8 // indirect
	github.com/gofiber/utils/v2 v2.6.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/golang/mock v1.6.0 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/influxdata/line-protocol/v2 v2.2.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/johnfercher/go-tree v1.1.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/lithammer/shortuuid/v3 v3.0.7 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/mitchellh/hashstructure/v2 v2.0.2 // indirect
	github.com/molecule-man/go-brrr v1.3.0 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nexus-rpc/nexus-proto-annotations v0.1.0 // indirect
	github.com/nexus-rpc/sdk-go v0.7.0 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/oklog/ulid/v2 v2.1.2 // indirect
	github.com/pdfcpu/pdfcpu v0.11.1 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/phpdave11/gofpdf v1.4.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.29 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	github.com/richardlehane/mscfb v1.0.7 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/samber/go-singleflightx v0.3.2 // indirect
	github.com/sony/gobreaker v1.0.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	github.com/tmthrgd/go-hex v0.0.0-20190904060850-447a3041c3bc // indirect
	github.com/uptrace/opentelemetry-go-extra/otelsql v0.3.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	github.com/yeqown/reedsolomon v1.0.0 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.temporal.io/api v1.63.5 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260527015227-08cc5374adb3 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
