package service

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
		//StorageClass: types.StorageClassReducedRedundancy,
	})
	if err != nil {
		return "", fmt.Errorf("failed to presign upload: %w", err)
	}
	presignURL := presignResult.URL
	return presignURL, nil
}
