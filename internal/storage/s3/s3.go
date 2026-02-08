package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/tawanorg/claude-sync/internal/storage"
)

func init() {
	storage.NewS3 = New
}

// Client implements the storage.Storage interface for AWS S3
type Client struct {
	client *s3.Client
	bucket string
}

// New creates a new S3 storage client
func New(cfg *storage.StorageConfig) (storage.Storage, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		config.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	var client *s3.Client
	if cfg.Endpoint != "" {
		client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	} else {
		client = s3.NewFromConfig(awsCfg)
	}

	return &Client{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// Upload stores data with the given key
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return nil
}

// Download retrieves data for the given key
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
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

// Delete removes the object with the given key
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

// DeleteBatch removes multiple objects in a single operation
func (c *Client) DeleteBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	const maxBatchSize = 1000

	for i := 0; i < len(keys); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(keys) {
			end = len(keys)
		}

		batch := keys[i:end]
		objects := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objects[j] = types.ObjectIdentifier{
				Key: aws.String(key),
			}
		}

		_, err := c.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(c.bucket),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete batch: %w", err)
		}
	}

	return nil
}

// List returns all objects with the given prefix
func (c *Client) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	var objects []storage.ObjectInfo
	var continuationToken *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.bucket),
			ContinuationToken: continuationToken,
		}
		if prefix != "" {
			input.Prefix = aws.String(prefix)
		}

		result, err := c.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range result.Contents {
			objects = append(objects, storage.ObjectInfo{
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

// Head returns metadata for the given key without downloading content
func (c *Client) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	result, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	return &storage.ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(result.ContentLength),
		LastModified: aws.ToTime(result.LastModified),
		ETag:         aws.ToString(result.ETag),
	}, nil
}

// BucketExists checks if the configured bucket exists
func (c *Client) BucketExists(ctx context.Context) (bool, error) {
	_, err := c.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		return false, nil
	}
	return true, nil
}
