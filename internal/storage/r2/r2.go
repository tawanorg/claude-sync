package r2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/tawanorg/claude-sync/internal/storage"
)

// partSize is the size of each part in a multipart upload/download (64 MiB).
// R2 supports up to 10,000 parts per upload, so 64 MiB parts handle objects
// up to ~640 GiB. This is large enough to keep part counts low (reducing API
// calls and completion overhead) while small enough that a single failed part
// doesn't waste too much bandwidth on retry.
const partSize = 64 * 1024 * 1024

// uploadConcurrency controls how many parts are uploaded in parallel. Kept
// low (2) to avoid saturating residential upload bandwidth, which causes
// Cloudflare to reset connections mid-transfer.
const uploadConcurrency = 2

// downloadConcurrency controls how many parts are downloaded in parallel.
// Download bandwidth is typically much higher than upload, so we can afford
// more parallelism.
const downloadConcurrency = 5

func init() {
	storage.NewR2 = New
}

// Client implements the storage.Storage interface for Cloudflare R2
type Client struct {
	client     *s3.Client
	uploader   *manager.Uploader
	downloader *manager.Downloader
	bucket     string
}

// New creates a new R2 storage client
func New(cfg *storage.StorageConfig) (storage.Storage, error) {
	endpoint := cfg.GetEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("R2 endpoint could not be determined")
	}

	// Build an HTTP client tuned for large multipart transfers to R2.
	//
	// Key issues addressed:
	//   - Cloudflare R2 endpoints resolve to both IPv4 and IPv6 addresses.
	//     Some networks have unreliable IPv6 connectivity that drops sustained
	//     uploads mid-stream ("broken pipe", "use of closed network connection").
	//     We force IPv4 via "tcp4" to avoid this.
	//   - HTTP/2 stream multiplexing can trigger Cloudflare stream resets on
	//     long-lived uploads, so we disable it.
	//   - Idle connections are aggressively reaped to prevent the SDK from
	//     reusing a connection closed by the remote end.
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		}
		t.ForceAttemptHTTP2 = false
		t.IdleConnTimeout = 30 * time.Second
		t.ResponseHeaderTimeout = 300 * time.Second
		t.ExpectContinueTimeout = 5 * time.Second
		t.MaxIdleConnsPerHost = downloadConcurrency + uploadConcurrency
	})

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		config.WithRegion("auto"),
		config.WithHTTPClient(httpClient),
		config.WithRetryer(func() aws.Retryer {
			return retry.AddWithMaxAttempts(retry.NewStandard(), 5)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Opts := func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		// R2 rejects the default x-amz-checksum headers that the SDK sends
		// when RequestChecksumCalculation is WhenSupported. Relax to
		// WhenRequired so the SDK only adds checksums when S3 mandates them.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	}

	client := s3.NewFromConfig(awsCfg, s3Opts)

	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = partSize
		u.Concurrency = uploadConcurrency
	})

	downloader := manager.NewDownloader(client, func(d *manager.Downloader) {
		d.PartSize = partSize
		d.Concurrency = downloadConcurrency
	})

	return &Client{
		client:     client,
		uploader:   uploader,
		downloader: downloader,
		bucket:     cfg.Bucket,
	}, nil
}

// Upload stores data with the given key. For objects larger than the configured
// part size the manager automatically uses S3 multipart upload, splitting the
// data into parts uploaded concurrently and reassembled server-side.
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	_, err := c.uploader.Upload(ctx, &s3.PutObjectInput{
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

// Download retrieves data for the given key. For large objects the manager
// issues concurrent ranged GET requests and reassembles the parts in memory.
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	buf := manager.NewWriteAtBuffer([]byte{})
	_, err := c.downloader.Download(ctx, buf, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	return buf.Bytes(), nil
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
		var notFound *types.NotFound
		var noSuchBucket *types.NoSuchBucket
		if errors.As(err, &notFound) || errors.As(err, &noSuchBucket) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check bucket: %w", err)
	}
	return true, nil
}
