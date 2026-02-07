package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	appconfig "github.com/tawanorg/claude-sync/internal/config"
)

type R2Client struct {
	client *s3.Client
	bucket string
}

type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

func NewR2Client(cfg *appconfig.Config) (*R2Client, error) {
	r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: cfg.Endpoint,
		}, nil
	})

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithEndpointResolverWithOptions(r2Resolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		config.WithRegion("auto"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	return &R2Client{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

func (r *R2Client) Upload(ctx context.Context, key string, data []byte) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return nil
}

func (r *R2Client) Download(ctx context.Context, key string) ([]byte, error) {
	result, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	defer result.Body.Close()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", key, err)
	}

	return data, nil
}

func (r *R2Client) Delete(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

func (r *R2Client) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var objects []ObjectInfo
	var continuationToken *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket:            aws.String(r.bucket),
			ContinuationToken: continuationToken,
		}
		if prefix != "" {
			input.Prefix = aws.String(prefix)
		}

		result, err := r.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range result.Contents {
			objects = append(objects, ObjectInfo{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
				ETag:         aws.ToString(obj.ETag),
			})
		}

		if !aws.ToBool(result.IsTruncated) {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return objects, nil
}

func (r *R2Client) Head(ctx context.Context, key string) (*ObjectInfo, error) {
	result, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	return &ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(result.ContentLength),
		LastModified: aws.ToTime(result.LastModified),
		ETag:         aws.ToString(result.ETag),
	}, nil
}

func (r *R2Client) CreateBucket(ctx context.Context) error {
	_, err := r.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(r.bucket),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraintUsWest2,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}
	return nil
}

func (r *R2Client) BucketExists(ctx context.Context) (bool, error) {
	_, err := r.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(r.bucket),
	})
	if err != nil {
		return false, nil
	}
	return true, nil
}
