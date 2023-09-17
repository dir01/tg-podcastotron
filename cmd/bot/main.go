package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/hori-ryota/zaperr"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"os/signal"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"tg-podcastotron/auth"
	"tg-podcastotron/bot"
	"tg-podcastotron/mediary"
	"tg-podcastotron/service"
	jobsqueue "tg-podcastotron/service/jobs_queue"
)

func main() {
	_ = godotenv.Load()
	logger, err := zap.NewDevelopment()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// region env vars
	mustGetEnv := func(key string) string {
		value, ok := os.LookupEnv(key)
		if !ok {
			logger.Fatal("missing env var", zap.String("key", key))
		}
		return value
	}
	botToken := mustGetEnv("BOT_TOKEN")
	adminUsername := mustGetEnv("ADMIN_USERNAME")
	mediaryURL := mustGetEnv("MEDIARY_URL")
	redisURL := mustGetEnv("REDIS_URL")
	bgJobsRedisURL := os.Getenv("REDIS_URL_BG_JOBS")
	if bgJobsRedisURL == "" {
		bgJobsRedisURL = redisURL
	}
	awsRegion := mustGetEnv("AWS_REGION")
	awsAccessKeyID := mustGetEnv("AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := mustGetEnv("AWS_SECRET_ACCESS_KEY")
	awsBucketName := mustGetEnv("AWS_BUCKET_NAME")
	userPathSecret := mustGetEnv("USER_PATH_SECRET") // just some random string, we'll use it to salt user id and take a hash as part of the path
	defaultFeedTitle := os.Getenv("DEFAULT_FEED_TITLE")
	// endregion

	// region redis
	mkRedisClient := func(url string) (client *redis.Client, teardown func()) {
		opt, err := redis.ParseURL(url)
		if err != nil {
			logger.Fatal("error parsing redis url", zaperr.ToField(err))
		}
		redisClient := redis.NewClient(opt)
		if _, err := redisClient.Ping(ctx).Result(); err != nil {
			logger.Fatal("error connecting to redis", zaperr.ToField(err))
		}
		return redisClient, func() {
			err := redisClient.Close()
			if err != nil {
				logger.Error("error closing redis client", zaperr.ToField(err))
			}
		}
	}
	bgJobsRedisClient, cleanupBgJobsRedisClient := mkRedisClient(bgJobsRedisURL)
	defer cleanupBgJobsRedisClient()
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
		logger.Fatal("error creating s3 config", zaperr.ToField(err))
	}

	if endpoint := os.Getenv("AWS_ENDPOINT"); endpoint != "" {
		cfg.EndpointResolverWithOptions = aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...any) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               endpoint,
				HostnameImmutable: true,
			}, nil
		})
	}

	s3Client := s3.NewFromConfig(cfg)
	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(awsBucketName),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(awsRegion),
		},
	})
	logger.Debug("created bucket", zap.String("bucket", awsBucketName), zaperr.ToField(err))
	// endregion

	// region jobs queue
	jobsQueue, err := jobsqueue.NewRedisJobsQueue(bgJobsRedisClient, 2, "undercast:jobs", logger)
	if err != nil {
		logger.Fatal("error creating jobs queue", zaperr.ToField(err))
	}
	// endregion

	mediaryService := mediary.New(mediaryURL, logger)
	db, err := sql.Open("sqlite3", "./db/sqlite.db")
	if err != nil {
		logger.Fatal("error opening db", zaperr.ToField(err))
	}
	svcRepo := service.NewSqliteRepository(db)
	s3Store := service.NewS3Store(s3Client, awsBucketName)
	obfuscateIDs := func(id string) string {
		hash := sha256.Sum256([]byte(userPathSecret + id))
		return hex.EncodeToString(hash[:])
	}
	svc := service.New(mediaryService, svcRepo, s3Store, jobsQueue, defaultFeedTitle, obfuscateIDs, logger)

	botStore := bot.NewSqliteRepository(db)
	authRepo := auth.NewSqliteRepository(db)
	botAuthService := auth.New(adminUsername, authRepo, logger)
	ubot := bot.NewUndercastBot(botToken, botAuthService, botStore, svc, logger)
	if err := ubot.Start(ctx); err != nil {
		logger.Fatal("error starting bot", zaperr.ToField(err))
	}
}
