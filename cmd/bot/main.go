package main

import (
	"context"
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
	"undercast-bot/auth"
	"undercast-bot/bot"
	"undercast-bot/mediary"
	"undercast-bot/service"
	"undercast-bot/service/jobs_queue"
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
	awsRegion := mustGetEnv("AWS_REGION")
	awsAccessKeyID := mustGetEnv("AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := mustGetEnv("AWS_SECRET_ACCESS_KEY")
	awsBucketName := mustGetEnv("AWS_BUCKET_NAME")
	userPathSecret := mustGetEnv("USER_PATH_SECRET") // just some random string, we'll use it to salt user id and take a hash as part of the path
	// endregion

	// region redis
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Fatal("error parsing redis url", zap.Error(err))
	}
	redisClient := redis.NewClient(opt)
	defer func() {
		err := redisClient.Close()
		if err != nil {
			logger.Error("error closing redis client", zap.Error(err))
		}
	}()
	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		logger.Fatal("error connecting to redis", zap.Error(err))
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
		logger.Fatal("error creating s3 config", zap.Error(err))
	}
	s3Client := s3.NewFromConfig(cfg)
	_, _ = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(awsBucketName),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(awsRegion),
		},
	})
	// endregion

	// region jobs queue
	jobsQueue, err := jobsqueue.NewRedisJobsQueue(redisClient, 10, "undercast:jobs", logger)
	if err != nil {
		logger.Fatal("error creating jobs queue", zap.Error(err))
	}
	// endregion

	mediaryService := mediary.New(mediaryURL, logger)
	svcRepo := service.NewRepository(redisClient, "undercast:service")
	s3Store := service.NewS3Store(s3Client, awsBucketName)
	svc := service.New(mediaryService, svcRepo, s3Store, jobsQueue, userPathSecret, logger)

	botStore := bot.NewRedisStore(redisClient, "undercast:bot")
	authRepo := auth.NewRepository(redisClient, "undercast:auth")
	botAuthService := auth.New(adminUsername, authRepo, logger)
	ubot := bot.NewUndercastBot(botToken, botAuthService, botStore, svc, logger)
	if err := ubot.Start(ctx); err != nil {
		logger.Fatal("error starting bot", zap.Error(err))
	}
}
