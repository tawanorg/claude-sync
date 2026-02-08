package storage

import (
	"testing"
)

func TestStorageConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  StorageConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty config",
			config:  StorageConfig{},
			wantErr: true,
			errMsg:  "bucket is required",
		},
		{
			name: "missing provider",
			config: StorageConfig{
				Bucket: "test-bucket",
			},
			wantErr: true,
			errMsg:  "provider is required",
		},
		{
			name: "invalid provider",
			config: StorageConfig{
				Provider: "invalid",
				Bucket:   "test-bucket",
			},
			wantErr: true,
			errMsg:  "unsupported provider",
		},
		// R2 tests
		{
			name: "valid R2 config",
			config: StorageConfig{
				Provider:        ProviderR2,
				Bucket:          "test-bucket",
				AccountID:       "account123",
				AccessKeyID:     "access123",
				SecretAccessKey: "secret123",
			},
			wantErr: false,
		},
		{
			name: "R2 missing account ID",
			config: StorageConfig{
				Provider:        ProviderR2,
				Bucket:          "test-bucket",
				AccessKeyID:     "access123",
				SecretAccessKey: "secret123",
			},
			wantErr: true,
			errMsg:  "account_id is required",
		},
		{
			name: "R2 missing access key",
			config: StorageConfig{
				Provider:        ProviderR2,
				Bucket:          "test-bucket",
				AccountID:       "account123",
				SecretAccessKey: "secret123",
			},
			wantErr: true,
			errMsg:  "access_key_id is required",
		},
		{
			name: "R2 missing secret key",
			config: StorageConfig{
				Provider:    ProviderR2,
				Bucket:      "test-bucket",
				AccountID:   "account123",
				AccessKeyID: "access123",
			},
			wantErr: true,
			errMsg:  "secret_access_key is required",
		},
		// S3 tests
		{
			name: "valid S3 config",
			config: StorageConfig{
				Provider:        ProviderS3,
				Bucket:          "test-bucket",
				AccessKeyID:     "access123",
				SecretAccessKey: "secret123",
				Region:          "us-east-1",
			},
			wantErr: false,
		},
		{
			name: "S3 missing access key",
			config: StorageConfig{
				Provider:        ProviderS3,
				Bucket:          "test-bucket",
				SecretAccessKey: "secret123",
				Region:          "us-east-1",
			},
			wantErr: true,
			errMsg:  "access_key_id is required",
		},
		{
			name: "S3 missing secret key",
			config: StorageConfig{
				Provider:    ProviderS3,
				Bucket:      "test-bucket",
				AccessKeyID: "access123",
				Region:      "us-east-1",
			},
			wantErr: true,
			errMsg:  "secret_access_key is required",
		},
		{
			name: "S3 missing region",
			config: StorageConfig{
				Provider:        ProviderS3,
				Bucket:          "test-bucket",
				AccessKeyID:     "access123",
				SecretAccessKey: "secret123",
			},
			wantErr: true,
			errMsg:  "region is required",
		},
		// GCS tests
		{
			name: "valid GCS config with ADC",
			config: StorageConfig{
				Provider:  ProviderGCS,
				Bucket:    "test-bucket",
				ProjectID: "project123",
			},
			wantErr: false,
		},
		{
			name: "valid GCS config with credentials file",
			config: StorageConfig{
				Provider:        ProviderGCS,
				Bucket:          "test-bucket",
				ProjectID:       "project123",
				CredentialsFile: "/path/to/creds.json",
			},
			wantErr: false,
		},
		{
			name: "GCS missing project ID",
			config: StorageConfig{
				Provider: ProviderGCS,
				Bucket:   "test-bucket",
			},
			wantErr: true,
			errMsg:  "project_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestStorageConfig_GetEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		config   StorageConfig
		expected string
	}{
		{
			name: "R2 with account ID",
			config: StorageConfig{
				Provider:  ProviderR2,
				AccountID: "abc123",
			},
			expected: "https://abc123.r2.cloudflarestorage.com",
		},
		{
			name: "R2 with custom endpoint",
			config: StorageConfig{
				Provider:  ProviderR2,
				AccountID: "abc123",
				Endpoint:  "https://custom.endpoint.com",
			},
			expected: "https://custom.endpoint.com",
		},
		{
			name: "S3 with region",
			config: StorageConfig{
				Provider: ProviderS3,
				Region:   "us-west-2",
			},
			expected: "https://s3.us-west-2.amazonaws.com",
		},
		{
			name: "S3 with custom endpoint",
			config: StorageConfig{
				Provider: ProviderS3,
				Region:   "us-west-2",
				Endpoint: "https://custom.s3.endpoint.com",
			},
			expected: "https://custom.s3.endpoint.com",
		},
		{
			name: "GCS returns empty (uses default)",
			config: StorageConfig{
				Provider: ProviderGCS,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetEndpoint()
			if got != tt.expected {
				t.Errorf("GetEndpoint() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
