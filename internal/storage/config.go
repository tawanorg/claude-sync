package storage

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// regionPattern matches AWS-style region identifiers (e.g. us-east-1, eu-central-1)
// and Backblaze B2 regions (e.g. us-west-004), used to extract a signing region
// from an S3-compatible endpoint host.
var regionPattern = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d+$`)

// NormalizeEndpoint ensures an S3-compatible endpoint is an absolute URI.
// The AWS SDK's BaseEndpoint requires a scheme, so a bare host such as
// "s3.eu-central-003.backblazeb2.com" is prefixed with "https://". Endpoints
// that already carry a scheme (http:// or https://) are returned unchanged.
func NormalizeEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if !strings.Contains(endpoint, "://") {
		return "https://" + endpoint
	}
	return endpoint
}

// RegionFromEndpoint derives the signing region from an S3-compatible endpoint URL.
// Providers like Backblaze B2 and Wasabi encode the region in the host as
// s3.<region>.<provider>.com; SigV4 needs that exact region. When the region
// cannot be confidently extracted (R2, MinIO, custom hosts) it returns "auto",
// which works for providers that ignore the region. Returns "" for an empty endpoint.
func RegionFromEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}

	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}

	labels := strings.Split(host, ".")
	if len(labels) >= 2 && labels[0] == "s3" && regionPattern.MatchString(labels[1]) {
		return labels[1]
	}
	return "auto"
}

// StorageConfig holds configuration for any storage provider
type StorageConfig struct {
	Provider Provider `yaml:"provider"`
	Bucket   string   `yaml:"bucket"`

	// R2/S3 common fields
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`
	Region          string `yaml:"region,omitempty"`

	// R2-specific
	AccountID string `yaml:"account_id,omitempty"`

	// GCS-specific
	ProjectID             string `yaml:"project_id,omitempty"`
	CredentialsFile       string `yaml:"credentials_file,omitempty"`
	CredentialsJSON       string `yaml:"credentials_json,omitempty"`
	UseDefaultCredentials bool   `yaml:"use_default_credentials,omitempty"`

	// WebDAV-specific (Nextcloud, ownCloud, etc.)
	WebDAVURL      string `yaml:"webdav_url,omitempty"`
	WebDAVUsername string `yaml:"webdav_username,omitempty"`
	WebDAVPassword string `yaml:"webdav_password,omitempty"`
	PathPrefix     string `yaml:"path_prefix,omitempty"`

	// Azure Blob Storage - SAS URL including container name and token
	AzureURL string `yaml:"azure_url,omitempty"`
}

// Validate checks if the configuration is valid for the selected provider
func (c *StorageConfig) Validate() error {
	if c.Provider != ProviderWebDAV && c.Provider != ProviderAzure && c.Bucket == "" {
		return fmt.Errorf("bucket is required")
	}

	switch c.Provider {
	case ProviderR2:
		return c.validateR2()
	case ProviderS3:
		return c.validateS3()
	case ProviderGCS:
		return c.validateGCS()
	case ProviderWebDAV:
		return c.validateWebDAV()
	case ProviderAzure:
		return c.validateAzure()
	case "":
		return fmt.Errorf("provider is required")
	default:
		return fmt.Errorf("unsupported provider: %s", c.Provider)
	}
}

func (c *StorageConfig) validateR2() error {
	if c.AccountID == "" {
		return fmt.Errorf("account_id is required for R2")
	}
	if c.AccessKeyID == "" {
		return fmt.Errorf("access_key_id is required for R2")
	}
	if c.SecretAccessKey == "" {
		return fmt.Errorf("secret_access_key is required for R2")
	}
	return nil
}

func (c *StorageConfig) validateS3() error {
	if c.AccessKeyID == "" {
		return fmt.Errorf("access_key_id is required for S3")
	}
	if c.SecretAccessKey == "" {
		return fmt.Errorf("secret_access_key is required for S3")
	}
	if c.Region == "" {
		return fmt.Errorf("region is required for S3")
	}
	return nil
}

func (c *StorageConfig) validateGCS() error {
	if c.ProjectID == "" {
		return fmt.Errorf("project_id is required for GCS")
	}
	// GCS can use default credentials, credentials file, or JSON
	// At least one auth method should be available (or use_default_credentials)
	return nil
}

func (c *StorageConfig) validateWebDAV() error {
	if c.WebDAVURL == "" {
		return fmt.Errorf("webdav_url is required for WebDAV")
	}
	if c.WebDAVUsername == "" {
		return fmt.Errorf("webdav_username is required for WebDAV")
	}
	if c.WebDAVPassword == "" {
		return fmt.Errorf("webdav_password is required for WebDAV")
	}
	return nil
}

func (c *StorageConfig) validateAzure() error {
	if c.AzureURL == "" {
		return fmt.Errorf("azure_url is required for Azure Blob Storage")
	}
	if !strings.HasPrefix(c.AzureURL, "https://") {
		return fmt.Errorf("azure_url must start with https://")
	}
	return nil
}

// GetEndpoint returns the endpoint URL for the storage provider
func (c *StorageConfig) GetEndpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}

	switch c.Provider {
	case ProviderR2:
		if c.AccountID != "" {
			return fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.AccountID)
		}
	case ProviderS3:
		if c.Region != "" {
			return fmt.Sprintf("https://s3.%s.amazonaws.com", c.Region)
		}
	}

	return ""
}
