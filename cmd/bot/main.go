package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws" //nolint:staticcheck // deprecated without a replacement; TODO: move to smithy-go OTel tracing
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"tg-podcastotron/auth"
	"tg-podcastotron/bot"
	"tg-podcastotron/mediary"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/telemetry"
	"tg-podcastotron/tgminiapp"
)

func main() {
	_ = godotenv.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// region telemetry
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "tg-podcastotron"
	}
	telemetryInstance, err := telemetry.Initialize(ctx, telemetry.Config{
		ServiceName:    serviceName,
		ServiceVersion: os.Getenv("OTEL_SERVICE_VERSION"),
		Environment:    os.Getenv("ENVIRONMENT"),
		OTLPEndpoint:   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		EnableStdout:   os.Getenv("ENVIRONMENT") != "production",
	})
	if err != nil {
		slog.ErrorContext(ctx, "error initializing telemetry", slog.Any("error", err))
		os.Exit(1)
	}
	defer telemetryInstance.Shutdown(ctx) //nolint:errcheck
	logger := telemetryInstance.Logger

	metrics, err := telemetry.NewMetrics()
	if err != nil {
		logger.ErrorContext(ctx, "error creating metrics", slog.Any("error", err))
		os.Exit(1)
	}
	// endregion

	// region env vars
	mustGetEnv := func(key string) string {
		value, ok := os.LookupEnv(key)
		if !ok {
			logger.ErrorContext(ctx, "missing env var", slog.String("key", key))
			os.Exit(1)
		}
		return value
	}
	botToken := mustGetEnv("BOT_TOKEN")
	adminUsername := mustGetEnv("ADMIN_USERNAME")
	mediaryURL := mustGetEnv("MEDIARY_URL")
	awsRegion := mustGetEnv("AWS_REGION")
	awsAccessKeyID := mustGetEnv("AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := mustGetEnv("AWS_SECRET_ACCESS_KEY")
	awsBucketName := mustGetEnv("AWS_BUCKET_NAME")
	userPathSecret := mustGetEnv("USER_PATH_SECRET") // just some random string, we'll use it to salt user id and take a hash as part of the path
	miniAppURL := strings.TrimRight(mustGetEnv("TG_MINI_APP_URL"), "/")
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	defaultFeedTitle := os.Getenv("DEFAULT_FEED_TITLE")
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./db/sqlite.db"
	}
	// endregion

	// region s3 client
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(awsRegion),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     awsAccessKeyID,
				SecretAccessKey: awsSecretAccessKey,
			},
		}),
	)
	if err != nil {
		logger.ErrorContext(ctx, "error creating s3 config", slog.Any("error", err))
		os.Exit(1)
	}

	otelaws.AppendMiddlewares(&cfg.APIOptions)

	if endpoint := os.Getenv("AWS_ENDPOINT"); endpoint != "" {
		// this is utilized by localstack
		_ = os.Setenv("AWS_ENDPOINT_URL_S3", endpoint)
	}

	s3Client := s3.NewFromConfig(cfg)
	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(awsBucketName),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(awsRegion),
		},
	})
	logger.DebugContext(ctx, "created bucket", slog.String("bucket", awsBucketName), slog.Any("error", err))
	// endregion

	mediaryHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	mediaryService := mediary.New(mediaryURL, mediaryHTTPClient, logger)

	// WAL + a busy timeout let concurrent writers (the jobs queue workers and
	// the publish path) wait for the write lock instead of failing immediately
	// with "database is locked". WAL is also required by litestream replication.
	//
	// db keeps the default *deferred* transaction locking: sqlq runs each job
	// handler inside a transaction on db but writes nothing to it until the
	// handler returns. With deferred locking that wrapping tx holds no write
	// lock while the handler runs, so the handler's own writes (each on a
	// separate pooled connection) can acquire the write lock. _txlock=immediate
	// on db would grab the lock at BEGIN and self-deadlock the handler against
	// its own queue transaction.
	//
	// The service repository's own transactions read and then write. A deferred
	// transaction in WAL mode cannot wait for the write lock when another
	// connection commits between its read and its write: SQLite returns
	// SQLITE_BUSY immediately, and the busy timeout does not apply. Thus these
	// transactions run on txDB, which takes the write lock at BEGIN and waits
	// for it with the busy timeout.
	dsn := "file:" + dbPath + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := otelsql.Open("sqlite3", dsn,
		otelsql.WithAttributes(semconv.DBSystemSqlite),
	)
	if err != nil {
		logger.ErrorContext(ctx, "error opening db", slog.Any("error", err))
		os.Exit(1)
	}
	txDB, err := otelsql.Open("sqlite3", dsn+"&_txlock=immediate",
		otelsql.WithAttributes(semconv.DBSystemSqlite),
	)
	if err != nil {
		logger.ErrorContext(ctx, "error opening tx db", slog.Any("error", err))
		os.Exit(1)
	}
	otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemSqlite)) //nolint:errcheck

	// region jobs queue
	jobsQueue, err := jobsqueue.New(db, 2, logger)
	if err != nil {
		logger.ErrorContext(ctx, "error creating jobs queue", slog.Any("error", err))
		os.Exit(1)
	}
	// endregion

	svcRepo := service.NewSqliteRepository(db, txDB)
	s3Store := service.NewS3Store(s3Client, awsBucketName)
	obfuscateIDs := func(id string) string {
		hash := sha256.Sum256([]byte(userPathSecret + id))
		return hex.EncodeToString(hash[:])
	}
	svc := service.New(mediaryService, svcRepo, s3Store, jobsQueue, defaultFeedTitle, obfuscateIDs, logger, metrics)
	if _, err := service.RegisterEpisodeGauges(telemetry.Meter(), svcRepo, logger, time.Now); err != nil {
		logger.ErrorContext(ctx, "error registering episode gauges", slog.Any("error", err))
		os.Exit(1)
	}

	botStore := bot.NewSqliteRepository(db)
	authRepo := auth.NewSqliteRepository(db)
	authService := auth.New(adminUsername, authRepo, logger)

	// The bot gets links before the Mini App exists; it uses the Mini App only after Start.
	var miniApp *tgminiapp.Server
	ubot, err := bot.NewUndercastBot(bot.Config{
		Token:      botToken,
		MiniAppURL: miniAppURL,
		Prefetch:   func(ctx context.Context, url string) { miniApp.Prefetch(ctx, url) },
	}, authService, botStore, svc, logger)
	if err != nil {
		logger.ErrorContext(ctx, "error creating bot", slog.Any("error", err))
		os.Exit(1)
	}
	botUsername, err := ubot.Username(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "error getting bot username", slog.Any("error", err))
		os.Exit(1)
	}

	// region mini app
	miniAppConfig := tgminiapp.Config{BotToken: botToken, BotUsername: botUsername}
	if devUserID := os.Getenv("TG_MINI_APP_DEV_USER_ID"); devUserID != "" && os.Getenv("ENVIRONMENT") != "production" {
		// Lets a normal browser open the Mini App as this user. Never in production.
		miniAppConfig.DevUser = &tgminiapp.DevUser{ID: devUserID, Username: os.Getenv("TG_MINI_APP_DEV_USERNAME")}
		logger.WarnContext(ctx, "mini app accepts requests without telegram init data", slog.String("dev_user_id", devUserID))
	}
	miniApp, err = tgminiapp.New(miniAppConfig, svc, authService, logger)
	if err != nil {
		logger.ErrorContext(ctx, "error creating mini app", slog.Any("error", err))
		os.Exit(1)
	}
	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           otelhttp.NewHandler(miniApp.Handler(), "tgminiapp"),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.InfoContext(ctx, "mini app listening", slog.String("addr", httpAddr), slog.String("url", miniAppURL))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "mini app server failed", slog.Any("error", err))
			cancel()
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	// endregion

	statusChanges := svc.Start(ctx)
	go svc.RunExpiredEpisodesCleanup(ctx, time.NewTicker(24*time.Hour).C)
	ubot.Start(ctx, statusChanges)
}
