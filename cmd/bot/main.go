package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"os/signal"

	"github.com/XSAM/otelsql"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"tg-podcastotron/auth"
	"tg-podcastotron/bot"
	"tg-podcastotron/mediary"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
	"tg-podcastotron/telemetry"
)

func main() {
	_ = godotenv.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// region telemetry
	telemetryInstance, err := telemetry.Initialize(ctx, telemetry.Config{
		ServiceName:    "tg-podcastotron",
		ServiceVersion: "1.0.0",
		Environment:    os.Getenv("ENVIRONMENT"),
		OTLPEndpoint:   os.Getenv("OTEL_ENDPOINT"),
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

	mediaryService := mediary.New(mediaryURL, logger)

	db, err := otelsql.Open("sqlite3", dbPath,
		otelsql.WithAttributes(semconv.DBSystemSqlite),
	)
	if err != nil {
		logger.ErrorContext(ctx, "error opening db", slog.Any("error", err))
		os.Exit(1)
	}
	otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(semconv.DBSystemSqlite)) //nolint:errcheck

	jobsQueue, err := jobsqueue.New(db, 2, logger)
	if err != nil {
		logger.ErrorContext(ctx, "error creating jobs queue", slog.Any("error", err))
		os.Exit(1)
	}

	svcRepo := service.NewSqliteRepository(db)
	s3Store := service.NewS3Store(s3Client, awsBucketName)
	obfuscateIDs := func(id string) string {
		hash := sha256.Sum256([]byte(userPathSecret + id))
		return hex.EncodeToString(hash[:])
	}
	svc := service.New(mediaryService, svcRepo, s3Store, jobsQueue, defaultFeedTitle, obfuscateIDs, logger, metrics)

	botStore := bot.NewSqliteRepository(db)
	authRepo := auth.NewSqliteRepository(db)
	botAuthService := auth.New(adminUsername, authRepo, logger)
	ubot := bot.NewUndercastBot(botToken, botAuthService, botStore, svc, logger)
	if err := ubot.Start(ctx); err != nil {
		logger.ErrorContext(ctx, "error starting bot", slog.Any("error", err))
		os.Exit(1)
	}
}
