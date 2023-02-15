package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func NewS3Store(s3Client *s3.Client, bucketName string) S3Store {
	return &s3Store{
		s3Client:   s3Client,
		bucketName: bucketName,
	}
}

type s3Store struct {
	s3Client   *s3.Client
	bucketName string
}

func (store *s3Store) PreSignedURL(key string) (string, error) {
	presignClient := s3.NewPresignClient(store.s3Client)
	presignResult, err := presignClient.PresignPutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(store.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(48*time.Hour))
	if err != nil {
		return "", fmt.Errorf("failed to presign upload: %w", err)
	}
	presignURL := presignResult.URL
	return presignURL, nil
}

type PutOptions struct {
	ContentType string
}

func WithContentType(contentType string) func(*PutOptions) {
	return func(opts *PutOptions) {
		opts.ContentType = contentType
	}
}

func (store *s3Store) Put(ctx context.Context, key string, dataReader io.ReadSeeker, opts ...func(*PutOptions)) error {
	options := &PutOptions{}
	for _, opt := range opts {
		opt(options)
	}

	putObjectInput := &s3.PutObjectInput{
		Bucket: aws.String(store.bucketName),
		Key:    aws.String(key),
		Body:   dataReader,
		ACL:    types.ObjectCannedACLPublicRead,
	}
	if options.ContentType != "" {
		putObjectInput.ContentType = aws.String(options.ContentType)
	}
	_, err := store.s3Client.PutObject(ctx, putObjectInput)
	if err != nil {
		return fmt.Errorf("failed to put object: %w", err)
	}
	return nil
}
