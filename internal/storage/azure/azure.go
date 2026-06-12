package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	acontainer "github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"

	storage "github.com/tawanorg/claude-sync/internal/storage"
)

// AzureProvider implements Storage using Azure Blob Storage with a container-scoped SAS URL.
type AzureProvider struct {
	client        *acontainer.Client
	containerName string
}

func init() {
	storage.NewAzure = New
}

// New creates an AzureProvider from a StorageConfig containing a container-scoped SAS URL.
func New(cfg *storage.StorageConfig) (storage.Storage, error) {
	parsed, err := url.Parse(cfg.AzureURL)
	if err != nil {
		return nil, fmt.Errorf("invalid azure_url: %w", err)
	}
	containerName := strings.TrimPrefix(parsed.Path, "/")
	if idx := strings.Index(containerName, "/"); idx != -1 {
		containerName = containerName[:idx]
	}
	if containerName == "" {
		return nil, errors.New("azure_url must include container name in path")
	}
	client, err := acontainer.NewClientWithNoCredential(cfg.AzureURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating azure container client: %w", err)
	}
	return &AzureProvider{client: client, containerName: containerName}, nil
}

func (a *AzureProvider) Upload(ctx context.Context, key string, data []byte) error {
	_, err := a.client.NewBlockBlobClient(key).UploadBuffer(ctx, data, nil)
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return nil
}

func (a *AzureProvider) Download(ctx context.Context, key string) ([]byte, error) {
	resp, err := a.client.NewBlobClient(key).DownloadStream(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (a *AzureProvider) Delete(ctx context.Context, key string) error {
	_, err := a.client.NewBlobClient(key).Delete(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

func (a *AzureProvider) DeleteBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	for _, key := range keys {
		if err := a.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (a *AzureProvider) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	opts := &acontainer.ListBlobsFlatOptions{}
	if prefix != "" {
		opts.Prefix = &prefix
	}
	pager := a.client.NewListBlobsFlatPager(opts)

	var results []storage.ObjectInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Segment.BlobItems {
			info := storage.ObjectInfo{Key: *item.Name}
			if item.Properties != nil {
				if item.Properties.ContentLength != nil {
					info.Size = *item.Properties.ContentLength
				}
				if item.Properties.LastModified != nil {
					info.LastModified = *item.Properties.LastModified
				}
			}
			results = append(results, info)
		}
	}
	return results, nil
}

func (a *AzureProvider) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	resp, err := a.client.NewBlobClient(key).GetProperties(ctx, nil)
	if err != nil {
		if is404(err) {
			return nil, nil
		}
		return nil, err
	}
	info := &storage.ObjectInfo{Key: key}
	if resp.ContentLength != nil {
		info.Size = *resp.ContentLength
	}
	if resp.LastModified != nil {
		info.LastModified = *resp.LastModified
	}
	return info, nil
}

func (a *AzureProvider) BucketExists(ctx context.Context) (bool, error) {
	_, err := a.client.GetProperties(ctx, nil)
	if err != nil {
		if is404(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check container: %w", err)
	}
	return true, nil
}

func is404(err error) bool {
	var respErr *azcore.ResponseError
	return errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound
}
