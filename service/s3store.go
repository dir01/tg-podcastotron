package service

import (
	"context"
	"fmt"
	"io"
	"strings"
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

func (store *s3Store) URL(key string) (url string, err error) {
	// TODO: there surely must be a more sane way to do this
	presignURL, err := store.PreSignedURL(key)
	if err != nil {
		return "", fmt.Errorf("s3store failed to get presigned url: %w", err)
	}
	url = stripQuery(presignURL)
	return url, nil
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

func (store *s3Store) Delete(ctx context.Context, key string) error {
	_, err := store.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(store.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

// List returns all objects whose key starts with prefix.
func (store *s3Store) List(ctx context.Context, prefix string) ([]S3Object, error) {
	var result []S3Object
	paginator := s3.NewListObjectsV2Paginator(store.s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(store.bucketName),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}
		for _, obj := range page.Contents {
			result = append(result, S3Object{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
			})
		}
	}
	return result, nil
}

// DeleteMany deletes the given objects, up to 1000 per request.
func (store *s3Store) DeleteMany(ctx context.Context, keys []string) error {
	const batchSize = 1000 // S3 DeleteObjects limit
	for start := 0; start < len(keys); start += batchSize {
		batch := keys[start:min(start+batchSize, len(keys))]
		objects := make([]types.ObjectIdentifier, len(batch))
		for i, key := range batch {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}
		out, err := store.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(store.bucketName),
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("failed to delete objects: %w", err)
		}
		if len(out.Errors) > 0 {
			e := out.Errors[0]
			return fmt.Errorf("failed to delete %d objects, e.g. %s: %s", len(out.Errors), aws.ToString(e.Key), aws.ToString(e.Message))
		}
	}
	return nil
}

func stripQuery(url string) string {
	if i := strings.Index(url, "?"); i != -1 {
		return url[:i]
	}
	return url
}
