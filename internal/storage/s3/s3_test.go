package s3

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/tawanorg/claude-sync/internal/storage"
)

func TestBuildS3Options_CustomEndpoint(t *testing.T) {
	cfg := &storage.StorageConfig{
		Endpoint: "https://s3.us-west-004.backblazeb2.com",
	}

	opts := &awss3.Options{}
	buildS3Options(cfg)(opts)

	if opts.BaseEndpoint == nil || *opts.BaseEndpoint != cfg.Endpoint {
		t.Errorf("BaseEndpoint = %v, want %q", opts.BaseEndpoint, cfg.Endpoint)
	}
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationWhenRequired {
		t.Errorf("RequestChecksumCalculation = %v, want WhenRequired", opts.RequestChecksumCalculation)
	}
	if opts.ResponseChecksumValidation != aws.ResponseChecksumValidationWhenRequired {
		t.Errorf("ResponseChecksumValidation = %v, want WhenRequired", opts.ResponseChecksumValidation)
	}
}

func TestBuildS3Options_EndpointWithoutSchemeNormalized(t *testing.T) {
	cfg := &storage.StorageConfig{
		Endpoint: "s3.eu-central-003.backblazeb2.com", // no scheme
	}

	opts := &awss3.Options{}
	buildS3Options(cfg)(opts)

	want := "https://s3.eu-central-003.backblazeb2.com"
	if opts.BaseEndpoint == nil || *opts.BaseEndpoint != want {
		t.Errorf("BaseEndpoint = %v, want %q", opts.BaseEndpoint, want)
	}
}

func TestBuildS3Options_AWSDefaultUnchanged(t *testing.T) {
	cfg := &storage.StorageConfig{
		Region:       "us-east-1", // real AWS S3: no custom endpoint
		UsePathStyle: true,        // should not be considered
	}

	opts := &awss3.Options{}
	buildS3Options(cfg)(opts)

	if opts.BaseEndpoint != nil {
		t.Errorf("BaseEndpoint = %v, want nil for AWS", *opts.BaseEndpoint)
	}
	if opts.RequestChecksumCalculation != aws.RequestChecksumCalculationUnset {
		t.Errorf("RequestChecksumCalculation = %v, want Unset (AWS default preserved)", opts.RequestChecksumCalculation)
	}
	if opts.ResponseChecksumValidation != aws.ResponseChecksumValidationUnset {
		t.Errorf("ResponseChecksumValidation = %v, want Unset (AWS default preserved)", opts.ResponseChecksumValidation)
	}
	if opts.UsePathStyle != false {
		t.Errorf("UsePathStyle = %v, want false (AWS default preserved)", opts.UsePathStyle)
	}
}
