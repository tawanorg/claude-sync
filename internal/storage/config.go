package storage

import (
	"fmt"
)

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
}

// Validate checks if the configuration is valid for the selected provider
func (c *StorageConfig) Validate() error {
	if c.Bucket == "" {
		return fmt.Errorf("bucket is required")
	}

	switch c.Provider {
	case ProviderR2:
		return c.validateR2()
	case ProviderS3:
		return c.validateS3()
	case ProviderGCS:
		return c.validateGCS()
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
