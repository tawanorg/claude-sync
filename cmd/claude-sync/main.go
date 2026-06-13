package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"
	"github.com/tawanorg/claude-sync/internal/sync"
	"github.com/tawanorg/claude-sync/internal/util"

	// Register storage adapters
	_ "github.com/tawanorg/claude-sync/internal/storage/gcs"
	_ "github.com/tawanorg/claude-sync/internal/storage/r2"
	_ "github.com/tawanorg/claude-sync/internal/storage/s3"
	_ "github.com/tawanorg/claude-sync/internal/storage/webdav"
)

var (
	version = "dev" // Set via ldflags at build time: -ldflags "-X main.version=x.x.x"
	quiet   bool
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "claude-sync",
		Short:   "Sync Claude Code sessions across devices",
		Long:    `A CLI tool to sync your ~/.claude directory across devices using cloud storage with encryption.`,
		Version: version,
	}

	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output")

	rootCmd.AddCommand(
		initCmd(),
		pushCmd(),
		pullCmd(),
		statusCmd(),
		diffCmd(),
		conflictsCmd(),
		resetCmd(),
		migrateCmd(),
		updateCmd(),
		changelogCmd(),
		mcpCmd(),
		pathsCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func printBanner() {
	fmt.Println()
	fmt.Printf("  %sWelcome to Claude Sync!%s %sv%s%s\n", colorBold, colorReset, colorDim, version, colorReset)
	fmt.Println()

	// Block-style ASCII art - CLAUDE SYNC on one line
	fmt.Printf("%s", colorCyan)
	fmt.Println("  ██████╗██╗      █████╗ ██╗   ██╗██████╗ ███████╗  ███████╗██╗   ██╗███╗   ██╗ ██████╗")
	fmt.Println("  ██╔════╝██║     ██╔══██╗██║   ██║██╔══██╗██╔════╝  ██╔════╝╚██╗ ██╔╝████╗  ██║██╔════╝")
	fmt.Println("  ██║     ██║     ███████║██║   ██║██║  ██║█████╗    ███████╗ ╚████╔╝ ██╔██╗ ██║██║     ")
	fmt.Println("  ██║     ██║     ██╔══██║██║   ██║██║  ██║██╔══╝    ╚════██║  ╚██╔╝  ██║╚██╗██║██║     ")
	fmt.Println("  ╚██████╗███████╗██║  ██║╚██████╔╝██████╔╝███████╗  ███████║   ██║   ██║ ╚████║╚██████╗")
	fmt.Println("   ╚═════╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝  ╚══════╝   ╚═╝   ╚═╝  ╚═══╝ ╚═════╝")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("  %sSync your Claude Code sessions across all your devices.%s\n", colorDim, colorReset)
	fmt.Printf("  %sIssues & PRs welcome: %shttps://github.com/tawanorg/claude-sync%s\n", colorDim, colorCyan, colorReset)
	fmt.Println()
}

func printStep(step int, total int, text string) {
	fmt.Printf("\n%s[%d/%d]%s %s%s%s\n", colorCyan, step, total, colorReset, colorBold, text, colorReset)
}

func printInfo(text string) {
	fmt.Printf("      %s%s%s\n", colorDim, text, colorReset)
}

func printSuccess(text string) {
	fmt.Printf("  %s%s%s\n", colorGreen, text, colorReset)
}

func printWarning(text string) {
	fmt.Printf("  %s%s%s\n", colorYellow, text, colorReset)
}

func initCmd() *cobra.Command {
	var provider, bucket string
	var scope string
	var usePassphrase, force bool

	// R2 flags
	var accountID, accessKey, secretKey string

	// S3 flags
	var s3Region string

	// S3-compatible (custom endpoint) flags
	var s3Endpoint string

	// GCS flags
	var gcsProjectID, gcsCredentialsFile string

	// WebDAV flags
	var webdavURL, webdavUsername, webdavPassword, webdavPathPrefix string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize claude-sync configuration",
		Long: `Set up cloud storage credentials and generate encryption keys.

Supported providers:
  - r2:            Cloudflare R2 (S3-compatible, free tier: 10GB)
  - s3:            Amazon S3
  - gcs:           Google Cloud Storage
  - s3-compatible: Any S3-compatible provider via custom endpoint (Backblaze B2, MinIO, Wasabi, ...)
  - webdav:        WebDAV (Nextcloud, ownCloud, etc. - self-hosted)

Examples:
  claude-sync init                # Full setup wizard
  claude-sync init --passphrase   # Re-enter passphrase only (keeps storage config)
  claude-sync init --force        # Reset everything, start fresh
  claude-sync init --provider s3-compatible --endpoint https://s3.us-west-004.backblazeb2.com   # Backblaze B2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Show banner
			printBanner()

			ctx := context.Background()
			keyPath := config.AgeKeyFilePath()

			// Special case: --passphrase with existing config = just regenerate key
			if usePassphrase && config.Exists() && !force {
				return initPassphraseOnly(ctx, keyPath)
			}

			// Normal flow: full setup
			return initFullSetup(ctx, keyPath, provider, bucket, accountID, accessKey, secretKey, s3Region, s3Endpoint, gcsProjectID, gcsCredentialsFile, webdavURL, webdavUsername, webdavPassword, webdavPathPrefix, scope, usePassphrase, force)
		},
	}

	// Provider selection
	cmd.Flags().StringVar(&provider, "provider", "", "Storage provider: r2, s3, gcs, s3-compatible, or webdav")
	cmd.Flags().StringVar(&scope, "scope", "", "Sync scope: 'full' (default, everything) or 'sessions' (conversation history only)")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Bucket name")
	cmd.Flags().BoolVar(&usePassphrase, "passphrase", false, "Derive encryption key from passphrase")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing config/key without prompting")

	// R2 flags
	cmd.Flags().StringVar(&accountID, "account-id", "", "Cloudflare Account ID (R2)")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "Access Key ID (R2/S3)")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "Secret Access Key (R2/S3)")

	// S3 flags
	cmd.Flags().StringVar(&s3Region, "region", "", "Region (S3 / S3-compatible)")

	// S3-compatible flags
	cmd.Flags().StringVar(&s3Endpoint, "endpoint", "", "Custom S3-compatible endpoint URL (e.g. https://s3.us-west-004.backblazeb2.com)")

	// GCS flags
	cmd.Flags().StringVar(&gcsProjectID, "project-id", "", "GCP Project ID (GCS)")
	cmd.Flags().StringVar(&gcsCredentialsFile, "credentials-file", "", "Path to GCS credentials JSON file")

	// WebDAV flags
	cmd.Flags().StringVar(&webdavURL, "webdav-url", "", "WebDAV URL (e.g. https://cloud.example.com/remote.php/dav/files/user/)")
	cmd.Flags().StringVar(&webdavUsername, "webdav-username", "", "WebDAV username")
	cmd.Flags().StringVar(&webdavPassword, "webdav-password", "", "WebDAV app password")
	cmd.Flags().StringVar(&webdavPathPrefix, "webdav-path-prefix", "claude-sync", "WebDAV path prefix (subdirectory)")

	return cmd
}

// initPassphraseOnly handles the case where user just wants to re-enter passphrase
// keeping existing storage configuration
func initPassphraseOnly(ctx context.Context, keyPath string) error {
	existingCfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}

	storageCfg := existingCfg.GetStorageConfig()
	store, err := storage.New(storageCfg)
	if err != nil {
		return fmt.Errorf("failed to connect to storage: %w", err)
	}

	fmt.Printf("  %sUsing existing storage config:%s %s/%s\n\n",
		colorDim, colorReset, storageCfg.Provider, storageCfg.Bucket)

	printInfo("Use the SAME passphrase on all devices.")

	shouldClearRemote, err := enterPassphraseAndVerify(ctx, store, keyPath)
	if err != nil {
		return err
	}

	// Clear remote if user chose to start fresh
	if shouldClearRemote {
		fmt.Printf("%s⋯%s Clearing remote files...\n", colorDim, colorReset)
		if err := clearRemoteStorage(ctx, store); err != nil {
			printWarning("Failed to clear remote: " + err.Error())
		} else {
			printSuccess("Remote files cleared")
		}
	}

	fmt.Println()
	fmt.Println(colorGreen + "  Passphrase updated!" + colorReset)
	fmt.Println()

	return nil
}

// resolveScope validates a --scope value, prompting interactively when empty.
// Returns "full" or "sessions". "sessions" syncs only portable conversation
// data; "full" syncs everything (the historical default).
func resolveScope(scope string) (string, error) {
	switch scope {
	case config.ScopeFull, config.ScopeSessions:
		return scope, nil
	case "":
		prompt := &survey.Select{
			Message: "What should be synced?",
			Options: []string{
				"Sessions only — conversation history (recommended for syncing across machines)",
				"Everything — settings, plugins, skills, agents, and sessions",
			},
		}
		var c int
		if err := survey.AskOne(prompt, &c); err != nil {
			return "", err
		}
		if c == 0 {
			return config.ScopeSessions, nil
		}
		return config.ScopeFull, nil
	default:
		return "", fmt.Errorf("invalid --scope %q (use \"full\" or \"sessions\")", scope)
	}
}

// initFullSetup handles the full init wizard
func initFullSetup(ctx context.Context, keyPath, provider, bucket, accountID, accessKey, secretKey, s3Region, s3Endpoint, gcsProjectID, gcsCredentialsFile, webdavURL, webdavUsername, webdavPassword, webdavPathPrefix, scope string, usePassphrase, force bool) error {
	if config.Exists() && !force {
		var overwrite bool
		prompt := &survey.Confirm{
			Message: "Configuration already exists. Overwrite?",
			Default: false,
		}
		if err := survey.AskOne(prompt, &overwrite); err != nil || !overwrite {
			fmt.Println("  Aborted.")
			return nil
		}
		fmt.Println()
	}

	// Step 1: Select provider
	printStep(1, 4, "Select Storage Provider")
	fmt.Println()

	if provider == "" {
		prompt := &survey.Select{
			Message: "Choose your cloud storage provider:",
			Options: []string{
				"Cloudflare R2 (recommended - free tier: 10GB)",
				"Amazon S3",
				"Google Cloud Storage",
				"S3-compatible (custom endpoint) — Backblaze B2, MinIO, Wasabi, ...",
				"WebDAV (Nextcloud, ownCloud, etc. - self-hosted)",
			},
		}
		var choice int
		if err := survey.AskOne(prompt, &choice); err != nil {
			return err
		}
		switch choice {
		case 0:
			provider = "r2"
		case 1:
			provider = "s3"
		case 2:
			provider = "gcs"
		case 3:
			provider = "s3-compatible"
		case 4:
			provider = "webdav"
		}
	}

	var storageCfg *storage.StorageConfig
	var err error
	fmt.Println()

	switch provider {
	case "r2":
		storageCfg, err = runR2Wizard(accountID, accessKey, secretKey, bucket)
	case "s3":
		storageCfg, err = runS3Wizard(accessKey, secretKey, s3Region, bucket)
	case "gcs":
		storageCfg, err = runGCSWizard(gcsProjectID, gcsCredentialsFile, bucket)
	case "s3-compatible":
		storageCfg, err = runS3CompatibleWizard(s3Endpoint, accessKey, secretKey, s3Region, bucket)
	case "webdav":
		storageCfg, err = runWebDAVWizard(webdavURL, webdavUsername, webdavPassword, webdavPathPrefix)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	if err != nil {
		return err
	}
	if storageCfg == nil {
		return fmt.Errorf("setup cancelled")
	}

	// Step 2: Encryption setup
	fmt.Println()
	printStep(2, 4, "Set Up Encryption")
	printInfo("Files are encrypted with 'age' before upload.")
	fmt.Println()

	configDir := config.ConfigDirPath()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Check if we should use passphrase mode
	if !usePassphrase && !crypto.KeyExists(keyPath) {
		prompt := &survey.Select{
			Message: "Choose encryption key method:",
			Options: []string{
				"Passphrase (recommended) - same key on all devices",
				"Random key - must copy key file to other devices",
			},
		}
		var choice int
		if err := survey.AskOne(prompt, &choice); err != nil {
			return err
		}
		usePassphrase = choice == 0
		fmt.Println()
	}

	// Create storage client early so we can verify key matches remote
	store, err := storage.New(storageCfg)
	if err != nil {
		if strings.Contains(err.Error(), "could not find default credentials") {
			printWarning("GCS Application Default Credentials not configured.")
			fmt.Println()
			printInfo("To set up ADC, run:")
			fmt.Printf("  %sgcloud auth application-default login%s\n", colorCyan, colorReset)
			fmt.Println()
			printInfo("Or use a service account JSON file instead.")
			return fmt.Errorf("GCS credentials not configured")
		}
		return fmt.Errorf("failed to create storage client: %w", err)
	}

	shouldClearRemote := false

	if usePassphrase {
		if crypto.KeyExists(keyPath) && !force {
			var overwriteKey bool
			prompt := &survey.Confirm{
				Message: "Encryption key already exists. Overwrite?",
				Default: false,
			}
			if err := survey.AskOne(prompt, &overwriteKey); err != nil || !overwriteKey {
				printSuccess("Using existing key")
				goto skipKeyGen
			}
		}

		printInfo("Use the SAME passphrase on all devices.")
		shouldClearRemote, err = enterPassphraseAndVerify(ctx, store, keyPath)
		if err != nil {
			return err
		}

	} else if !crypto.KeyExists(keyPath) {
		if err := crypto.GenerateKey(keyPath); err != nil {
			return fmt.Errorf("failed to generate key: %w", err)
		}
		printSuccess("Key generated: " + keyPath)
		printWarning("Back up this file! You need it on other devices.")
	} else {
		printSuccess("Using existing key")
	}
skipKeyGen:

	// Step 3: Test Connection
	fmt.Println()
	printStep(3, 4, "Test Connection")

	exists, err := store.BucketExists(ctx)
	if err != nil {
		return fmt.Errorf("could not verify bucket '%s': %w", storageCfg.Bucket, err)
	} else if !exists {
		if storageCfg.Provider == storage.ProviderWebDAV {
			return fmt.Errorf("could not access WebDAV path '%s' - check your URL and credentials", storageCfg.PathPrefix)
		}
		return fmt.Errorf("bucket '%s' does not exist. Please create it first in your storage provider's console.\n  For R2: https://dash.cloudflare.com/ → R2 → Create bucket\n  For S3: https://console.aws.amazon.com/s3/ → Create bucket (use 'automatic' location)\n  For GCS: https://console.cloud.google.com/storage/ → Create bucket", storageCfg.Bucket)
	}
	if storageCfg.Provider == storage.ProviderWebDAV {
		printSuccess("Connected to WebDAV ('" + storageCfg.PathPrefix + "')")
	} else {
		printSuccess("Connected to '" + storageCfg.Bucket + "'")
	}

	// Clear remote if user chose to start fresh
	if shouldClearRemote {
		fmt.Printf("%s⋯%s Clearing remote files...\n", colorDim, colorReset)
		if err := clearRemoteStorage(ctx, store); err != nil {
			printWarning("Failed to clear remote: " + err.Error())
		} else {
			printSuccess("Remote files cleared")
		}
	}

	// Verify encryption key can decrypt remote files (if any exist)
	if !shouldClearRemote {
		if err := verifyKeyMatchesRemote(ctx, store, keyPath); err != nil {
			fmt.Println()
			printWarning("Encryption key cannot decrypt remote files!")
			printInfo("The remote bucket has files encrypted with a different key.")
			fmt.Println()
			printInfo("Options:")
			printInfo("  1. Run 'claude-sync init --passphrase' to try a different passphrase")
			printInfo("  2. Copy the age-key.txt from your original device")
			printInfo("  3. Run 'claude-sync reset --remote' to clear remote and start fresh")
			fmt.Println()
			return fmt.Errorf("encryption key mismatch - cannot sync with remote")
		}
		printSuccess("Encryption key verified")
	}

	// Resolve sync scope (prompts if not provided via --scope)
	scope, err = resolveScope(scope)
	if err != nil {
		return err
	}

	// Save config
	cfg := &config.Config{
		Storage:       storageCfg,
		EncryptionKey: "~/.claude-sync/age-key.txt",
	}
	if scope == config.ScopeSessions {
		cfg.Scope = config.ScopeSessions
	}

	if err := config.Save(cfg); err != nil {
		return err
	}

	// Done
	fmt.Println()
	fmt.Println(colorGreen + "  Setup complete!" + colorReset)
	fmt.Println()
	printInfo("Run 'claude-sync push' to upload your sessions")
	printInfo("Run 'claude-sync pull' on other devices to sync")
	fmt.Println()

	return nil
}

// enterPassphraseAndVerify prompts for passphrase and verifies against remote
// Returns shouldClearRemote flag
func enterPassphraseAndVerify(ctx context.Context, store storage.Storage, keyPath string) (bool, error) {
	for {
		var passphrase string
		for {
			prompt := &survey.Password{
				Message: "Passphrase (min 8 chars):",
			}
			if err := survey.AskOne(prompt, &passphrase); err != nil {
				return false, err
			}

			if err := crypto.ValidatePassphraseStrength(passphrase); err != nil {
				printWarning(err.Error())
				continue
			}

			var confirm string
			confirmPrompt := &survey.Password{
				Message: "Confirm passphrase:",
			}
			if err := survey.AskOne(confirmPrompt, &confirm); err != nil {
				return false, err
			}

			if passphrase != confirm {
				printWarning("Passphrases don't match.")
				continue
			}
			break
		}

		if err := crypto.GenerateKeyFromPassphrase(keyPath, passphrase); err != nil {
			return false, fmt.Errorf("failed to generate key: %w", err)
		}

		// Verify the key matches existing remote files (if any)
		if err := verifyKeyMatchesRemote(ctx, store, keyPath); err != nil {
			// Key mismatch detected - ask user what to do
			action, actionErr := handleKeyMismatch()
			if actionErr != nil {
				return false, actionErr
			}

			switch action {
			case actionRetryPassphrase:
				os.Remove(keyPath)
				fmt.Println()
				printInfo("Enter a different passphrase:")
				continue
			case actionClearRemote:
				printSuccess("Key derived from passphrase")
				printInfo("Remote files will be cleared...")
				return true, nil
			case actionAbort:
				os.Remove(keyPath)
				return false, fmt.Errorf("setup aborted")
			}
		}

		printSuccess("Key derived from passphrase")
		return false, nil
	}
}

func runR2Wizard(accountID, accessKey, secretKey, bucket string) (*storage.StorageConfig, error) {
	fmt.Printf("  %sCloudflare R2 Setup%s\n\n", colorBold, colorReset)
	printInfo("You need a Cloudflare R2 bucket and API token.")
	printInfo("R2 free tier includes 10GB storage.")
	fmt.Println()
	fmt.Printf("  %s1.%s Create bucket: %shttps://dash.cloudflare.com/?to=/:account/r2/new%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	fmt.Printf("  %s2.%s Create API token: %shttps://dash.cloudflare.com/?to=/:account/r2/api-tokens%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	printInfo("   Select 'Object Read & Write' permission")
	fmt.Println()

	answers := struct {
		AccountID string
		AccessKey string
		SecretKey string
		Bucket    string
	}{
		AccountID: accountID,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Bucket:    bucket,
	}

	questions := []*survey.Question{
		{
			Name: "AccountID",
			Prompt: &survey.Input{
				Message: "Account ID:",
				Help:    "Found in URL: dash.cloudflare.com/<ACCOUNT_ID>/r2",
				Default: accountID,
			},
			Validate: survey.Required,
		},
		{
			Name: "AccessKey",
			Prompt: &survey.Input{
				Message: "Access Key ID:",
				Default: accessKey,
			},
			Validate: survey.Required,
		},
		{
			Name: "SecretKey",
			Prompt: &survey.Password{
				Message: "Secret Access Key:",
			},
			Validate: survey.Required,
		},
		{
			Name: "Bucket",
			Prompt: &survey.Input{
				Message: "Bucket name:",
				Default: "claude-sync",
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return nil, err
	}

	return &storage.StorageConfig{
		Provider:        storage.ProviderR2,
		Bucket:          answers.Bucket,
		AccountID:       answers.AccountID,
		AccessKeyID:     answers.AccessKey,
		SecretAccessKey: answers.SecretKey,
	}, nil
}

func runS3Wizard(accessKey, secretKey, region, bucket string) (*storage.StorageConfig, error) {
	fmt.Printf("  %sAmazon S3 Setup%s\n\n", colorBold, colorReset)
	printInfo("You need an AWS S3 bucket and IAM credentials.")
	fmt.Println()
	fmt.Printf("  %s1.%s Create bucket: %shttps://s3.console.aws.amazon.com/s3/bucket/create%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	fmt.Printf("  %s2.%s Create access keys: %shttps://console.aws.amazon.com/iam/home#/security_credentials%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	fmt.Println()

	answers := struct {
		AccessKey string
		SecretKey string
		Region    string
		Bucket    string
	}{
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    region,
		Bucket:    bucket,
	}

	questions := []*survey.Question{
		{
			Name: "AccessKey",
			Prompt: &survey.Input{
				Message: "Access Key ID:",
				Default: accessKey,
			},
			Validate: survey.Required,
		},
		{
			Name: "SecretKey",
			Prompt: &survey.Password{
				Message: "Secret Access Key:",
			},
			Validate: survey.Required,
		},
		{
			Name: "Region",
			Prompt: &survey.Select{
				Message: "AWS Region:",
				Options: []string{
					"us-east-1",
					"us-east-2",
					"us-west-1",
					"us-west-2",
					"eu-west-1",
					"eu-west-2",
					"eu-central-1",
					"ap-northeast-1",
					"ap-southeast-1",
					"ap-southeast-2",
				},
				Default: "us-east-1",
				Description: func(value string, index int) string {
					descriptions := map[string]string{
						"us-east-1":      "US East (N. Virginia)",
						"us-east-2":      "US East (Ohio)",
						"us-west-1":      "US West (N. California)",
						"us-west-2":      "US West (Oregon)",
						"eu-west-1":      "Europe (Ireland)",
						"eu-west-2":      "Europe (London)",
						"eu-central-1":   "Europe (Frankfurt)",
						"ap-northeast-1": "Asia Pacific (Tokyo)",
						"ap-southeast-1": "Asia Pacific (Singapore)",
						"ap-southeast-2": "Asia Pacific (Sydney)",
					}
					return descriptions[value]
				},
			},
		},
		{
			Name: "Bucket",
			Prompt: &survey.Input{
				Message: "Bucket name:",
				Default: "claude-sync",
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return nil, err
	}

	return &storage.StorageConfig{
		Provider:        storage.ProviderS3,
		Bucket:          answers.Bucket,
		AccessKeyID:     answers.AccessKey,
		SecretAccessKey: answers.SecretKey,
		Region:          answers.Region,
	}, nil
}

// runS3CompatibleWizard configures any S3-compatible provider (Backblaze B2,
// MinIO, Wasabi, DigitalOcean Spaces, ...) by collecting a custom endpoint URL.
// It reuses the S3 storage engine (ProviderS3 with Endpoint set); the engine
// relaxes checksum behavior for custom endpoints so these providers accept the
// uploads. The signing region is pre-filled from the endpoint when derivable.
func runS3CompatibleWizard(endpoint, accessKey, secretKey, region, bucket string) (*storage.StorageConfig, error) {
	fmt.Printf("  %sS3-Compatible Setup%s\n\n", colorBold, colorReset)
	printInfo("Works with any S3-compatible provider: Backblaze B2, MinIO, Wasabi, DigitalOcean Spaces, ...")
	fmt.Println()
	printInfo("You need the provider's S3 endpoint URL, an access key/secret, and a bucket.")
	fmt.Println()

	// Ask the endpoint first so the signing region can be derived from it.
	if endpoint == "" {
		q := &survey.Input{
			Message: "S3 endpoint URL:",
			Help:    "e.g. https://s3.us-west-004.backblazeb2.com (Backblaze B2)",
		}
		if err := survey.AskOne(q, &endpoint, survey.WithValidator(survey.Required)); err != nil {
			return nil, err
		}
	}

	if region == "" {
		region = storage.RegionFromEndpoint(endpoint)
	}

	answers := struct {
		AccessKey string
		SecretKey string
		Region    string
		Bucket    string
	}{
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    region,
		Bucket:    bucket,
	}

	questions := []*survey.Question{
		{
			Name: "AccessKey",
			Prompt: &survey.Input{
				Message: "Access Key ID:",
				Default: accessKey,
			},
			Validate: survey.Required,
		},
		{
			Name: "SecretKey",
			Prompt: &survey.Password{
				Message: "Secret Access Key:",
			},
			Validate: survey.Required,
		},
		{
			Name: "Region",
			Prompt: &survey.Input{
				Message: "Region:",
				Default: region,
				Help:    "Signing region, auto-detected from the endpoint. 'auto' works for providers that ignore it.",
			},
			Validate: survey.Required,
		},
		{
			Name: "Bucket",
			Prompt: &survey.Input{
				Message: "Bucket name:",
				Default: "claude-sync",
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return nil, err
	}

	return &storage.StorageConfig{
		Provider:        storage.ProviderS3,
		Bucket:          answers.Bucket,
		AccessKeyID:     answers.AccessKey,
		SecretAccessKey: answers.SecretKey,
		Region:          answers.Region,
		Endpoint:        storage.NormalizeEndpoint(endpoint),
	}, nil
}

func runGCSWizard(projectID, credentialsFile, bucket string) (*storage.StorageConfig, error) {
	fmt.Printf("  %sGoogle Cloud Storage Setup%s\n\n", colorBold, colorReset)
	printInfo("You need a GCS bucket and service account credentials.")
	fmt.Println()
	fmt.Printf("  %s1.%s Create bucket: %shttps://console.cloud.google.com/storage/create-bucket%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	fmt.Printf("  %s2.%s Create service account: %shttps://console.cloud.google.com/iam-admin/serviceaccounts%s\n",
		colorCyan, colorReset, colorDim, colorReset)
	printInfo("   Grant 'Storage Object Admin' role to the service account")
	fmt.Println()

	answers := struct {
		ProjectID  string
		AuthMethod string
		Bucket     string
	}{
		ProjectID: projectID,
		Bucket:    bucket,
	}

	questions := []*survey.Question{
		{
			Name: "ProjectID",
			Prompt: &survey.Input{
				Message: "GCP Project ID:",
				Default: projectID,
			},
			Validate: survey.Required,
		},
		{
			Name: "AuthMethod",
			Prompt: &survey.Select{
				Message: "Authentication method:",
				Options: []string{
					"Application Default Credentials (requires: gcloud auth application-default login)",
					"Service Account JSON file",
				},
			},
		},
		{
			Name: "Bucket",
			Prompt: &survey.Input{
				Message: "Bucket name:",
				Default: "claude-sync",
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return nil, err
	}

	cfg := &storage.StorageConfig{
		Provider:  storage.ProviderGCS,
		Bucket:    answers.Bucket,
		ProjectID: answers.ProjectID,
	}

	if strings.Contains(answers.AuthMethod, "Service Account") {
		var credPath string
		prompt := &survey.Input{
			Message: "Credentials JSON file path:",
			Help:    "Path to your service account JSON key file",
			Suggest: func(toComplete string) []string {
				files, _ := filepath.Glob(toComplete + "*")
				return files
			},
		}
		if err := survey.AskOne(prompt, &credPath, survey.WithValidator(func(ans interface{}) error {
			path := ans.(string)
			if path == "" {
				return fmt.Errorf("credentials file path is required")
			}
			// Expand ~ in path
			if len(path) > 0 && path[0] == '~' {
				home, _ := os.UserHomeDir()
				path = home + path[1:]
			}
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return fmt.Errorf("file does not exist: %s", path)
			}
			return nil
		})); err != nil {
			return nil, err
		}
		cfg.CredentialsFile = credPath
	} else {
		cfg.UseDefaultCredentials = true
	}

	return cfg, nil
}

func runWebDAVWizard(webdavURL, username, password, pathPrefix string) (*storage.StorageConfig, error) {
	fmt.Printf("  %sWebDAV Setup (Nextcloud, ownCloud, etc.)%s\n\n", colorBold, colorReset)
	printInfo("You need a WebDAV server URL, username, and an app password.")
	fmt.Println()
	fmt.Printf("  %sNextcloud:%s Go to Settings → Security → Devices & sessions\n", colorCyan, colorReset)
	printInfo("  to create an app password (recommended over your account password)")
	fmt.Println()
	fmt.Printf("  %sURL format:%s https://cloud.example.com/remote.php/dav/files/USERNAME/\n", colorCyan, colorReset)
	fmt.Println()

	answers := struct {
		URL        string
		Username   string
		Password   string
		PathPrefix string
	}{
		URL:        webdavURL,
		Username:   username,
		Password:   password,
		PathPrefix: pathPrefix,
	}

	questions := []*survey.Question{
		{
			Name: "URL",
			Prompt: &survey.Input{
				Message: "WebDAV URL:",
				Default: webdavURL,
				Help:    "For Nextcloud: https://your-server/remote.php/dav/files/USERNAME/",
			},
			Validate: survey.Required,
		},
		{
			Name: "Username",
			Prompt: &survey.Input{
				Message: "Username:",
				Default: username,
			},
			Validate: survey.Required,
		},
		{
			Name: "Password",
			Prompt: &survey.Password{
				Message: "App password:",
				Help:    "Use an app-specific password, not your account password",
			},
		},
		{
			Name: "PathPrefix",
			Prompt: &survey.Input{
				Message: "Path prefix (subdirectory for sync data):",
				Default: func() string {
					if pathPrefix != "" {
						return pathPrefix
					}
					return "claude-sync"
				}(),
			},
			Validate: survey.Required,
		},
	}

	if err := survey.Ask(questions, &answers); err != nil {
		return nil, err
	}

	if answers.Password == "" && password != "" {
		answers.Password = password
	}
	if answers.Password == "" {
		return nil, fmt.Errorf("app password is required")
	}

	cfg := &storage.StorageConfig{
		Provider:       storage.ProviderWebDAV,
		Bucket:         answers.PathPrefix,
		WebDAVURL:      answers.URL,
		WebDAVUsername: answers.Username,
		WebDAVPassword: answers.Password,
		PathPrefix:     answers.PathPrefix,
	}

	return cfg, nil
}

func pushCmd() *cobra.Command {
	var includeMCP bool

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Upload local changes to cloud storage",
		Long:  `Encrypt and upload changed files from ~/.claude to cloud storage.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			if !quiet {
				syncer.SetProgressFunc(func(event sync.ProgressEvent) {
					if event.Error != nil {
						fmt.Printf("\r%s✗%s %s: %v\n", colorYellow, colorReset, event.Path, event.Error)
						return
					}

					switch event.Action {
					case "scan":
						if event.Complete {
							fmt.Printf("\r%s✓%s No changes to push\n", colorGreen, colorReset)
						} else {
							fmt.Printf("%s⋯%s %s\n", colorDim, colorReset, event.Path)
						}
					case "upload":
						if event.Complete {
							// Final newline after progress
						} else {
							// Clear line and show progress
							progress := fmt.Sprintf("[%d/%d]", event.Current, event.Total)
							shortPath := util.TruncatePath(event.Path, 50)
							fmt.Printf("\r%s↑%s %s%s%s %s (%s)%s",
								colorCyan, colorReset,
								colorDim, progress, colorReset,
								shortPath, util.FormatSize(event.Size),
								strings.Repeat(" ", 10))
						}
					case "delete":
						shortPath := util.TruncatePath(event.Path, 50)
						fmt.Printf("\r%s✗%s [%d/%d] %s (deleted)%s\n",
							colorYellow, colorReset,
							event.Current, event.Total,
							shortPath,
							strings.Repeat(" ", 10))
					}
				})
			}

			ctx := context.Background()
			result, err := syncer.Push(ctx)
			if err != nil {
				return err
			}

			if !quiet {
				fmt.Println() // Clear the progress line

				if len(result.Uploaded) == 0 && len(result.Deleted) == 0 && len(result.Errors) == 0 {
					// Already printed "No changes"
				} else {
					// Summary
					var parts []string
					if len(result.Uploaded) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d uploaded%s", colorGreen, len(result.Uploaded), colorReset))
					}
					if len(result.Deleted) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d deleted%s", colorYellow, len(result.Deleted), colorReset))
					}
					if len(result.Errors) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d failed%s", colorYellow, len(result.Errors), colorReset))
					}
					if len(parts) > 0 {
						fmt.Printf("%s✓%s Push complete: %s\n", colorGreen, colorReset, strings.Join(parts, ", "))
					}

					if len(result.Errors) > 0 {
						fmt.Printf("\n%sErrors:%s\n", colorYellow, colorReset)
						for _, e := range result.Errors {
							fmt.Printf("  %s•%s %v\n", colorYellow, colorReset, e)
						}
					}
				}
			}

			// MCP sync if enabled
			if includeMCP || (cfg.MCPSync != nil && *cfg.MCPSync) {
				if err := runMCPPush(ctx, syncer); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&includeMCP, "include-mcp", false, "Also sync MCP server configs from ~/.claude.json")
	return cmd
}

func pullCmd() *cobra.Command {
	var dryRun, force, includeMCP bool

	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download remote changes from cloud storage",
		Long: `Download and decrypt changed files from cloud storage to ~/.claude.

On first pull with existing local files, you'll be prompted to confirm
before any files are overwritten. Use --dry-run to preview changes first.

Examples:
  claude-sync pull              # Pull with safety prompts
  claude-sync pull --dry-run    # Preview what would be changed
  claude-sync pull --force      # Skip confirmation prompts`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()

			// Check for first pull with existing local files
			if !syncer.HasState() {
				hasExisting, err := hasExistingClaudeFiles(cfg.Scope)
				if err != nil {
					return err
				}

				if hasExisting && !force {
					return handleFirstPullWithExistingFiles(ctx, syncer, dryRun, cfg.GetEffectiveSyncPaths())
				}
			}

			// Handle dry-run for normal pulls
			if dryRun {
				return showPullPreview(ctx, syncer)
			}

			if !quiet {
				syncer.SetProgressFunc(func(event sync.ProgressEvent) {
					if event.Error != nil {
						fmt.Printf("\r%s✗%s %s: %v\n", colorYellow, colorReset, event.Path, event.Error)
						return
					}

					switch event.Action {
					case "scan":
						if event.Complete {
							fmt.Printf("\r%s✓%s Already up to date\n", colorGreen, colorReset)
						} else {
							fmt.Printf("%s⋯%s %s\n", colorDim, colorReset, event.Path)
						}
					case "download":
						if event.Complete {
							// Final newline after progress
						} else {
							// Clear line and show progress
							progress := fmt.Sprintf("[%d/%d]", event.Current, event.Total)
							shortPath := util.TruncatePath(event.Path, 50)
							fmt.Printf("\r%s↓%s %s%s%s %s (%s)%s",
								colorGreen, colorReset,
								colorDim, progress, colorReset,
								shortPath, util.FormatSize(event.Size),
								strings.Repeat(" ", 10))
						}
					case "conflict":
						fmt.Printf("\r%s⚠%s Conflict: %s (saved as .conflict)\n",
							colorYellow, colorReset, event.Path)
					}
				})
			}

			result, err := syncer.Pull(ctx)
			if err != nil {
				return err
			}

			if !quiet {
				fmt.Println() // Clear the progress line

				if len(result.Downloaded) == 0 && len(result.Conflicts) == 0 && len(result.Errors) == 0 {
					// Already printed "Already up to date"
				} else {
					// Summary
					var parts []string
					if len(result.Downloaded) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d downloaded%s", colorGreen, len(result.Downloaded), colorReset))
					}
					if len(result.Conflicts) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d conflicts%s", colorYellow, len(result.Conflicts), colorReset))
					}
					if len(result.Errors) > 0 {
						parts = append(parts, fmt.Sprintf("%s%d failed%s", colorYellow, len(result.Errors), colorReset))
					}
					if len(parts) > 0 {
						fmt.Printf("%s✓%s Pull complete: %s\n", colorGreen, colorReset, strings.Join(parts, ", "))
					}

					if len(result.Conflicts) > 0 {
						fmt.Printf("\n%sConflicts (both local and remote changed):%s\n", colorYellow, colorReset)
						for _, c := range result.Conflicts {
							fmt.Printf("  %s•%s %s\n", colorYellow, colorReset, c)
						}
						fmt.Printf("\n%sLocal versions kept. Remote saved as .conflict files.%s\n", colorDim, colorReset)
						fmt.Printf("%sRun '%sclaude-sync conflicts%s%s' to review and resolve.%s\n", colorDim, colorCyan, colorReset, colorDim, colorReset)
					}

					if len(result.Errors) > 0 {
						fmt.Printf("\n%sErrors:%s\n", colorYellow, colorReset)
						for _, e := range result.Errors {
							fmt.Printf("  %s•%s %v\n", colorYellow, colorReset, e)
						}
					}
				}
			}

			// MCP sync if enabled
			if includeMCP || (cfg.MCPSync != nil && *cfg.MCPSync) {
				if err := runMCPPull(ctx, syncer); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be changed without making changes")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files without confirmation")
	cmd.Flags().BoolVar(&includeMCP, "include-mcp", false, "Also sync MCP server configs from ~/.claude.json")

	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show pending local changes",
		Long:  `Display files that have been added, modified, or deleted locally.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()
			changes, err := syncer.Status(ctx)
			if err != nil {
				return err
			}

			if len(changes) == 0 {
				fmt.Println("No local changes")
				return nil
			}

			fmt.Printf("%d change(s):\n\n", len(changes))

			var added, modified, deleted []sync.FileChange
			for _, c := range changes {
				switch c.Action {
				case "add":
					added = append(added, c)
				case "modify":
					modified = append(modified, c)
				case "delete":
					deleted = append(deleted, c)
				}
			}

			if len(added) > 0 {
				fmt.Println("New files:")
				for _, c := range added {
					fmt.Printf("  + %s (%s)\n", c.Path, util.FormatSize(c.LocalSize))
				}
				fmt.Println()
			}

			if len(modified) > 0 {
				fmt.Println("Modified files:")
				for _, c := range modified {
					fmt.Printf("  ~ %s (%s)\n", c.Path, util.FormatSize(c.LocalSize))
				}
				fmt.Println()
			}

			if len(deleted) > 0 {
				fmt.Println("Deleted files:")
				for _, c := range deleted {
					fmt.Printf("  - %s\n", c.Path)
				}
				fmt.Println()
			}

			state := syncer.GetState()
			if !state.LastPush.IsZero() {
				fmt.Printf("Last push: %s\n", state.LastPush.Format(time.RFC3339))
			}
			if !state.LastPull.IsZero() {
				fmt.Printf("Last pull: %s\n", state.LastPull.Format(time.RFC3339))
			}

			return nil
		},
	}
}

func diffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff",
		Short: "Show differences between local and remote",
		Long:  `Compare local ~/.claude with remote cloud storage.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()
			entries, err := syncer.Diff(ctx)
			if err != nil {
				return err
			}

			if len(entries) == 0 {
				fmt.Println("No files found")
				return nil
			}

			var localOnly, remoteOnly, modified, synced []sync.DiffEntry
			for _, e := range entries {
				switch e.Status {
				case "local_only":
					localOnly = append(localOnly, e)
				case "remote_only":
					remoteOnly = append(remoteOnly, e)
				case "modified":
					modified = append(modified, e)
				case "synced":
					synced = append(synced, e)
				}
			}

			if len(localOnly) > 0 {
				fmt.Printf("Local only (%d files):\n", len(localOnly))
				for _, e := range localOnly {
					fmt.Printf("  + %s (%s)\n", e.Path, util.FormatSize(e.LocalSize))
				}
				fmt.Println()
			}

			if len(remoteOnly) > 0 {
				fmt.Printf("Remote only (%d files):\n", len(remoteOnly))
				for _, e := range remoteOnly {
					fmt.Printf("  - %s (%s)\n", e.Path, util.FormatSize(e.RemoteSize))
				}
				fmt.Println()
			}

			if len(modified) > 0 {
				fmt.Printf("Modified (%d files):\n", len(modified))
				for _, e := range modified {
					fmt.Printf("  ~ %s (local: %s, remote: %s)\n", e.Path, util.FormatSize(e.LocalSize), util.FormatSize(e.RemoteSize))
				}
				fmt.Println()
			}

			fmt.Printf("Summary: %d synced, %d local only, %d remote only, %d modified\n",
				len(synced), len(localOnly), len(remoteOnly), len(modified))

			return nil
		},
	}
}

type conflictFile struct {
	ConflictPath string
	OriginalPath string
	Timestamp    string
}

func conflictsCmd() *cobra.Command {
	var listOnly bool
	var resolveAll string

	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List and resolve sync conflicts",
		Long: `Find and resolve conflicts from sync operations.

When both local and remote files change, the remote version is saved
as a .conflict file. Use this command to review and resolve them.

Examples:
  claude-sync conflicts              # Interactive resolution
  claude-sync conflicts --list       # Just list conflicts
  claude-sync conflicts --keep local # Keep all local versions
  claude-sync conflicts --keep remote # Keep all remote versions`,
		RunE: func(cmd *cobra.Command, args []string) error {
			claudeDir := config.ClaudeDir()

			// Find all .conflict files
			conflicts, err := findConflicts(claudeDir)
			if err != nil {
				return err
			}

			if len(conflicts) == 0 {
				fmt.Printf("%s✓%s No conflicts found\n", colorGreen, colorReset)
				return nil
			}

			fmt.Printf("%sFound %d conflict(s):%s\n\n", colorYellow, len(conflicts), colorReset)

			for i, c := range conflicts {
				relOriginal, _ := filepath.Rel(claudeDir, c.OriginalPath)
				fmt.Printf("  %s%d.%s %s\n", colorCyan, i+1, colorReset, relOriginal)
				fmt.Printf("     %sConflict from: %s%s\n", colorDim, c.Timestamp, colorReset)
			}
			fmt.Println()

			// List only mode
			if listOnly {
				return nil
			}

			// Load sync state to update after resolution
			state, err := sync.LoadState()
			if err != nil {
				return fmt.Errorf("failed to load sync state: %w", err)
			}

			// Batch resolve mode
			if resolveAll != "" {
				return batchResolveConflicts(conflicts, resolveAll, claudeDir, state)
			}

			// Interactive mode
			return interactiveResolveConflicts(conflicts, claudeDir, state)
		},
	}

	cmd.Flags().BoolVarP(&listOnly, "list", "l", false, "Only list conflicts, don't resolve")
	cmd.Flags().StringVar(&resolveAll, "keep", "", "Resolve all conflicts: 'local' or 'remote'")

	return cmd
}

func findConflicts(claudeDir string) ([]conflictFile, error) {
	var conflicts []conflictFile

	err := filepath.Walk(claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}

		// Look for .conflict. pattern
		if strings.Contains(filepath.Base(path), ".conflict.") {
			// Extract original path and timestamp
			// Format: filename.ext.conflict.20260208-095132
			parts := strings.Split(path, ".conflict.")
			if len(parts) == 2 {
				conflicts = append(conflicts, conflictFile{
					ConflictPath: path,
					OriginalPath: parts[0],
					Timestamp:    parts[1],
				})
			}
		}
		return nil
	})

	// Sort by timestamp (newest first)
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].Timestamp > conflicts[j].Timestamp
	})

	return conflicts, err
}

func batchResolveConflicts(conflicts []conflictFile, keep string, claudeDir string, state *sync.SyncState) error {
	keep = strings.ToLower(keep)
	if keep != "local" && keep != "remote" {
		return fmt.Errorf("--keep must be 'local' or 'remote'")
	}

	resolved := 0
	for _, c := range conflicts {
		if keep == "local" {
			// Delete conflict file, keep local
			if err := os.Remove(c.ConflictPath); err != nil {
				fmt.Printf("%s✗%s Failed to remove %s: %v\n", colorYellow, colorReset, c.ConflictPath, err)
				continue
			}
			fmt.Printf("%s✓%s Kept local: %s\n", colorGreen, colorReset, filepath.Base(c.OriginalPath))
		} else {
			// Replace local with conflict, delete conflict
			if err := os.Rename(c.ConflictPath, c.OriginalPath); err != nil {
				fmt.Printf("%s✗%s Failed to replace %s: %v\n", colorYellow, colorReset, c.OriginalPath, err)
				continue
			}
			fmt.Printf("%s✓%s Kept remote: %s\n", colorGreen, colorReset, filepath.Base(c.OriginalPath))
		}

		// Update state with the resolved file's hash
		relPath, _ := filepath.Rel(claudeDir, c.OriginalPath)
		if info, err := os.Stat(c.OriginalPath); err == nil {
			if hash, err := sync.HashFile(c.OriginalPath); err == nil {
				state.UpdateFile(relPath, info, hash)
				state.MarkUploaded(relPath)
			}
		}
		resolved++
	}

	// Save updated state
	if resolved > 0 {
		if err := state.Save(); err != nil {
			fmt.Printf("%s⚠%s Warning: failed to save state: %v\n", colorYellow, colorReset, err)
		}
	}

	fmt.Printf("\n%s✓%s Resolved %d conflict(s)\n", colorGreen, colorReset, resolved)
	return nil
}

func interactiveResolveConflicts(conflicts []conflictFile, claudeDir string, state *sync.SyncState) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("For each conflict, choose how to resolve:")
	fmt.Printf("  %s[l]%s Keep local  %s[r]%s Keep remote  %s[d]%s Show diff  %s[s]%s Skip  %s[q]%s Quit\n\n",
		colorCyan, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset,
		colorCyan, colorReset)

	resolved := 0
	for i, c := range conflicts {
		relOriginal, _ := filepath.Rel(claudeDir, c.OriginalPath)

		// Get file sizes for context
		localInfo, _ := os.Stat(c.OriginalPath)
		conflictInfo, _ := os.Stat(c.ConflictPath)

		localSize := int64(0)
		conflictSize := int64(0)
		if localInfo != nil {
			localSize = localInfo.Size()
		}
		if conflictInfo != nil {
			conflictSize = conflictInfo.Size()
		}

		fmt.Printf("%s[%d/%d]%s %s\n", colorCyan, i+1, len(conflicts), colorReset, relOriginal)
		fmt.Printf("        Local: %s  |  Remote: %s  |  Conflict from: %s\n",
			util.FormatSize(localSize), util.FormatSize(conflictSize), c.Timestamp)

	promptLoop:
		for {
			fmt.Printf("        %sResolve [l/r/d/s/q]:%s ", colorDim, colorReset)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))

			switch input {
			case "l", "local":
				// Keep local, delete conflict
				if err := os.Remove(c.ConflictPath); err != nil {
					fmt.Printf("        %s✗%s Error: %v\n", colorYellow, colorReset, err)
				} else {
					fmt.Printf("        %s✓%s Kept local version\n\n", colorGreen, colorReset)
					// Update state with the kept file's hash
					if info, err := os.Stat(c.OriginalPath); err == nil {
						if hash, err := sync.HashFile(c.OriginalPath); err == nil {
							state.UpdateFile(relOriginal, info, hash)
							state.MarkUploaded(relOriginal)
						}
					}
					resolved++
				}
				break promptLoop

			case "r", "remote":
				// Replace local with conflict
				if err := os.Rename(c.ConflictPath, c.OriginalPath); err != nil {
					fmt.Printf("        %s✗%s Error: %v\n", colorYellow, colorReset, err)
				} else {
					fmt.Printf("        %s✓%s Replaced with remote version\n\n", colorGreen, colorReset)
					// Update state with the new file's hash
					if info, err := os.Stat(c.OriginalPath); err == nil {
						if hash, err := sync.HashFile(c.OriginalPath); err == nil {
							state.UpdateFile(relOriginal, info, hash)
							state.MarkUploaded(relOriginal)
						}
					}
					resolved++
				}
				break promptLoop

			case "d", "diff":
				// Show diff
				showDiff(c.OriginalPath, c.ConflictPath)

			case "s", "skip":
				fmt.Printf("        %s→%s Skipped\n\n", colorDim, colorReset)
				break promptLoop

			case "q", "quit":
				// Save state before quitting if any conflicts were resolved
				if resolved > 0 {
					if err := state.Save(); err != nil {
						fmt.Printf("%s⚠%s Warning: failed to save state: %v\n", colorYellow, colorReset, err)
					}
				}
				fmt.Printf("\n%s✓%s Resolved %d of %d conflict(s)\n", colorGreen, colorReset, resolved, len(conflicts))
				return nil

			default:
				fmt.Printf("        %sInvalid choice. Use l/r/d/s/q%s\n", colorDim, colorReset)
			}
		}
	}

	// Save updated state
	if resolved > 0 {
		if err := state.Save(); err != nil {
			fmt.Printf("%s⚠%s Warning: failed to save state: %v\n", colorYellow, colorReset, err)
		}
	}

	fmt.Printf("%s✓%s Resolved %d of %d conflict(s)\n", colorGreen, colorReset, resolved, len(conflicts))
	return nil
}

func showDiff(localPath, conflictPath string) {
	// Try to use diff command
	cmd := exec.Command("diff", "-u", "--color=always", localPath, conflictPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println()
	fmt.Printf("        %s--- Local%s\n", colorGreen, colorReset)
	fmt.Printf("        %s+++ Remote (conflict)%s\n", colorCyan, colorReset)
	fmt.Println()

	if err := cmd.Run(); err != nil {
		// diff returns exit code 1 when files differ, which is expected
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Files differ, this is normal
		} else if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			// diff command failed
			fmt.Printf("        %sCould not run diff command%s\n", colorDim, colorReset)

			// Fall back to showing file sizes
			localInfo, _ := os.Stat(localPath)
			conflictInfo, _ := os.Stat(conflictPath)
			if localInfo != nil && conflictInfo != nil {
				fmt.Printf("        Local:  %s (%s)\n", localPath, util.FormatSize(localInfo.Size()))
				fmt.Printf("        Remote: %s (%s)\n", conflictPath, util.FormatSize(conflictInfo.Size()))
			}
		}
	}
	fmt.Println()
}

func resetCmd() *cobra.Command {
	var clearRemote, clearLocal, force bool

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset claude-sync (clear data and start fresh)",
		Long: `Reset claude-sync configuration and optionally clear remote/local data.

Use this if you forgot your passphrase or want to start fresh.

Examples:
  claude-sync reset                    # Clear local config only
  claude-sync reset --remote           # Also delete all files from cloud storage
  claude-sync reset --local            # Also clear local sync state
  claude-sync reset --remote --local   # Full reset (nuclear option)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)

			fmt.Println()
			printWarning("This will reset claude-sync:")
			fmt.Println()

			if clearRemote {
				fmt.Printf("  %s•%s Delete ALL files from cloud storage bucket\n", colorYellow, colorReset)
			}
			if clearLocal {
				fmt.Printf("  %s•%s Clear local sync state\n", colorYellow, colorReset)
			}
			fmt.Printf("  %s•%s Delete local config and encryption key\n", colorYellow, colorReset)
			fmt.Println()

			if !force {
				fmt.Printf("%sType 'reset' to confirm:%s ", colorYellow, colorReset)
				confirm, _ := reader.ReadString('\n')
				if strings.TrimSpace(confirm) != "reset" {
					fmt.Println("Aborted.")
					return nil
				}
				fmt.Println()
			}

			// Clear remote if requested
			if clearRemote {
				fmt.Printf("%s⋯%s Deleting remote files...\n", colorDim, colorReset)

				cfg, err := config.Load()
				if err != nil {
					printWarning("Could not load config: " + err.Error())
				} else {
					storageCfg := cfg.GetStorageConfig()
					store, err := storage.New(storageCfg)
					if err != nil {
						printWarning("Could not connect to storage: " + err.Error())
					} else {
						ctx := context.Background()
						objects, err := store.List(ctx, "")
						if err != nil {
							printWarning("Could not list objects: " + err.Error())
						} else {
							deleted := 0
							for _, obj := range objects {
								if err := store.Delete(ctx, obj.Key); err != nil {
									printWarning("Failed to delete " + obj.Key)
								} else {
									deleted++
								}
							}
							printSuccess(fmt.Sprintf("Deleted %d files from storage", deleted))
						}
					}
				}
			}

			// Clear local state if requested
			if clearLocal {
				statePath := config.StateFilePath()
				if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
					printWarning("Could not remove state file: " + err.Error())
				} else {
					printSuccess("Cleared local sync state")
				}
			}

			// Always clear config and key
			configDir := config.ConfigDirPath()
			if err := os.RemoveAll(configDir); err != nil {
				return fmt.Errorf("failed to remove config directory: %w", err)
			}
			printSuccess("Removed " + configDir)

			fmt.Println()
			printSuccess("Reset complete!")
			fmt.Println()
			printInfo("Run 'claude-sync init' to set up again with a new passphrase.")
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().BoolVar(&clearRemote, "remote", false, "Delete all files from cloud storage bucket")
	cmd.Flags().BoolVar(&clearLocal, "local", false, "Clear local sync state")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")

	return cmd
}

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Rewrite legacy remote keys to portable path-mapped keys",
		Long: `Re-upload this device's project files under portable, path-mapped remote
keys and delete the legacy machine-specific keys.

Older versions stored project sessions under keys derived from this machine's
absolute paths (e.g. projects/-Users-alice-my-app/...), so sessions pulled on
a device with a different username or layout were not resumable. New pushes
use portable tokens (e.g. projects/${HOME}-my-app/...); migrate converts
existing remote data in place.

Run this once on every device. Keys owned by another device are left for that
device's migrate run and reported as "left for other devices".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			if !quiet {
				syncer.SetProgressFunc(func(event sync.ProgressEvent) {
					if event.Error != nil {
						fmt.Printf("\r%s✗%s %s: %v\n", colorYellow, colorReset, event.Path, event.Error)
						return
					}
					if event.Action == "upload" && !event.Complete {
						progress := fmt.Sprintf("[%d/%d]", event.Current, event.Total)
						shortPath := util.TruncatePath(event.Path, 50)
						fmt.Printf("\r%s↻%s %s%s%s %s%s",
							colorCyan, colorReset,
							colorDim, progress, colorReset,
							shortPath,
							strings.Repeat(" ", 10))
					}
				})
				fmt.Printf("%s⋯%s Scanning remote for legacy keys...\n", colorDim, colorReset)
			}

			result, err := syncer.MigratePaths(context.Background())
			if err != nil {
				return err
			}

			if !quiet {
				fmt.Println()
				if len(result.Migrated) == 0 && len(result.Foreign) == 0 && len(result.Errors) == 0 {
					fmt.Printf("%s✓%s Nothing to migrate\n", colorGreen, colorReset)
					return nil
				}

				var parts []string
				if len(result.Migrated) > 0 {
					parts = append(parts, fmt.Sprintf("%s%d migrated%s", colorGreen, len(result.Migrated), colorReset))
				}
				if len(result.Foreign) > 0 {
					parts = append(parts, fmt.Sprintf("%s%d left for other devices%s", colorDim, len(result.Foreign), colorReset))
				}
				if len(result.Errors) > 0 {
					parts = append(parts, fmt.Sprintf("%s%d failed%s", colorYellow, len(result.Errors), colorReset))
				}
				fmt.Printf("%s✓%s Migration complete: %s\n", colorGreen, colorReset, strings.Join(parts, ", "))

				if len(result.Foreign) > 0 {
					fmt.Printf("\n%sRun 'claude-sync migrate' on your other devices to convert the remaining keys.%s\n", colorDim, colorReset)
				}
				if len(result.Errors) > 0 {
					fmt.Printf("\n%sErrors:%s\n", colorYellow, colorReset)
					for _, e := range result.Errors {
						fmt.Printf("  %s•%s %v\n", colorYellow, colorReset, e)
					}
				}
			}

			return nil
		},
	}

	return cmd
}

// GitHubRelease represents a GitHub release from the API
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func updateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update claude-sync to the latest version",
		Long: `Check for updates and automatically download the latest version.

Examples:
  claude-sync update          # Update to latest version
  claude-sync update --check  # Only check for updates, don't install`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s⋯%s Checking for updates...\n", colorDim, colorReset)

			// Get latest release from GitHub
			release, err := getLatestRelease()
			if err != nil {
				return fmt.Errorf("failed to check for updates: %w", err)
			}

			latestVersion := strings.TrimPrefix(release.TagName, "v")
			currentVersion := strings.TrimPrefix(version, "v")

			if latestVersion == currentVersion {
				fmt.Printf("%s✓%s Already up to date (v%s)\n", colorGreen, colorReset, currentVersion)
				return nil
			}

			// Compare versions (simple string comparison works for semver)
			if util.CompareVersions(currentVersion, latestVersion) >= 0 {
				fmt.Printf("%s✓%s Already up to date (v%s)\n", colorGreen, colorReset, currentVersion)
				return nil
			}

			fmt.Printf("%s↑%s New version available: %sv%s%s → %sv%s%s\n",
				colorCyan, colorReset,
				colorDim, currentVersion, colorReset,
				colorGreen, latestVersion, colorReset)

			if checkOnly {
				fmt.Printf("\n%sRun 'claude-sync update' to install%s\n", colorDim, colorReset)
				return nil
			}

			// Find the right asset for this OS/arch
			assetName := util.GetBinaryName(latestVersion)
			var downloadURL string
			for _, asset := range release.Assets {
				if asset.Name == assetName {
					downloadURL = asset.BrowserDownloadURL
					break
				}
			}

			if downloadURL == "" {
				return fmt.Errorf("no binary available for %s/%s", runtime.GOOS, runtime.GOARCH)
			}

			fmt.Printf("%s⋯%s Downloading %s...\n", colorDim, colorReset, assetName)

			// Download the new binary
			newBinary, err := downloadBinary(downloadURL)
			if err != nil {
				return fmt.Errorf("failed to download update: %w", err)
			}

			// Verify against the release's published checksums
			if err := verifyChecksum(release, assetName, newBinary); err != nil {
				return fmt.Errorf("refusing to install update: %w", err)
			}

			// Get current executable path
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to get executable path: %w", err)
			}
			execPath, err = filepath.EvalSymlinks(execPath)
			if err != nil {
				return fmt.Errorf("failed to resolve executable path: %w", err)
			}

			// Replace the current binary
			fmt.Printf("%s⋯%s Installing update...\n", colorDim, colorReset)
			if err := replaceBinary(execPath, newBinary); err != nil {
				return fmt.Errorf("failed to install update: %w", err)
			}

			fmt.Printf("%s✓%s Updated to v%s\n", colorGreen, colorReset, latestVersion)
			fmt.Printf("\n%sRestart claude-sync to use the new version%s\n", colorDim, colorReset)

			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for updates, don't install")

	return cmd
}

func getLatestRelease() (*GitHubRelease, error) {
	url := "https://api.github.com/repos/tawanorg/claude-sync/releases/latest"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "claude-sync/"+version)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// verifyChecksum validates the downloaded binary against the checksums.txt
// asset published with the release. Releases predating checksum publication
// warn instead of failing; once checksums.txt is present, a missing entry or
// a mismatch aborts the update.
func verifyChecksum(release *GitHubRelease, assetName string, data []byte) error {
	var checksumsURL string
	for _, asset := range release.Assets {
		if asset.Name == "checksums.txt" {
			checksumsURL = asset.BrowserDownloadURL
			break
		}
	}
	if checksumsURL == "" {
		fmt.Printf("%s!%s Release has no checksums.txt; skipping integrity verification\n", colorYellow, colorReset)
		return nil
	}

	body, err := downloadBinary(checksumsURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums.txt: %w", err)
	}

	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])

	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum format: "<hash>  <filename>" ("*" prefix = binary mode)
		if strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}
		if !strings.EqualFold(fields[0], got) {
			return fmt.Errorf("sha256 mismatch for %s: release lists %s, downloaded %s", assetName, fields[0], got)
		}
		fmt.Printf("%s✓%s Checksum verified\n", colorGreen, colorReset)
		return nil
	}
	return fmt.Errorf("checksums.txt has no entry for %s", assetName)
}

func downloadBinary(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func replaceBinary(execPath string, newBinary []byte) error {
	// Write to a temporary file first
	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
		return fmt.Errorf("failed to write new binary: %w", err)
	}

	// Backup current binary
	backupPath := execPath + ".old"
	if err := os.Rename(execPath, backupPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// Move new binary into place
	if err := os.Rename(tmpPath, execPath); err != nil {
		// Try to restore backup (ignore error, we're already failing)
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Remove backup
	os.Remove(backupPath)

	return nil
}

// verifyKeyMatchesRemote checks if the encryption key can decrypt existing remote files.
// Returns nil if no files exist or if decryption succeeds.
// Returns an error if files exist but cannot be decrypted with the current key.
func verifyKeyMatchesRemote(ctx context.Context, store storage.Storage, keyPath string) error {
	// List remote files
	objects, err := store.List(ctx, "")
	if err != nil || len(objects) == 0 {
		return nil // No files to verify, or error listing (will fail later anyway)
	}

	// Find a small file to test with (prefer smaller files for faster verification)
	var testObj storage.ObjectInfo
	for _, obj := range objects {
		if obj.Size > 0 && obj.Size < 10000 { // Pick a small file under 10KB
			testObj = obj
			break
		}
	}
	if testObj.Key == "" && len(objects) > 0 {
		testObj = objects[0] // Fallback to first file
	}
	if testObj.Key == "" {
		return nil // No suitable file found
	}

	// Download the test file
	encrypted, err := store.Download(ctx, testObj.Key)
	if err != nil {
		return nil // Download failed, will fail later during pull
	}

	// Try to decrypt with current key
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		return fmt.Errorf("failed to load encryption key: %w", err)
	}

	_, err = enc.Decrypt(encrypted)
	if err != nil {
		return fmt.Errorf("key_mismatch: cannot decrypt remote files with current key")
	}

	return nil
}

// keyMismatchAction represents the user's choice when a key mismatch is detected
type keyMismatchAction int

const (
	actionRetryPassphrase keyMismatchAction = iota
	actionClearRemote
	actionAbort
)

// handleKeyMismatch displays a helpful error message and prompts the user for action
func handleKeyMismatch() (keyMismatchAction, error) {
	fmt.Println()
	printWarning("Cannot decrypt existing remote files!")
	fmt.Println()
	printInfo("The bucket contains files encrypted with a different key.")
	printInfo("This happens when:")
	printInfo("  - You used a different passphrase on another device")
	printInfo("  - You previously set up with a random key (not passphrase)")
	fmt.Println()

	prompt := &survey.Select{
		Message: "What would you like to do?",
		Options: []string{
			"Try a different passphrase",
			"Clear remote files and start fresh (your local ~/.claude will be pushed)",
			"Abort setup",
		},
	}
	var choice int
	if err := survey.AskOne(prompt, &choice); err != nil {
		return actionAbort, err
	}

	switch choice {
	case 0:
		return actionRetryPassphrase, nil
	case 1:
		return actionClearRemote, nil
	default:
		return actionAbort, nil
	}
}

// clearRemoteStorage deletes all files from the remote storage bucket
func clearRemoteStorage(ctx context.Context, store storage.Storage) error {
	objects, err := store.List(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list remote files: %w", err)
	}

	if len(objects) == 0 {
		return nil
	}

	keys := make([]string, len(objects))
	for i, obj := range objects {
		keys[i] = obj.Key
	}

	return store.DeleteBatch(ctx, keys)
}

// hasExistingClaudeFiles checks if ~/.claude has any files that would be synced
func hasExistingClaudeFiles(scope string) (bool, error) {
	claudeDir := config.ClaudeDir()
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		return false, nil
	}

	files, err := sync.GetLocalFiles(claudeDir, config.ScopedSyncPaths(scope))
	if err != nil {
		return false, err
	}

	return len(files) > 0, nil
}

// handleFirstPullWithExistingFiles handles the case where the user is pulling
// for the first time but already has local files that could be overwritten
func handleFirstPullWithExistingFiles(ctx context.Context, syncer *sync.Syncer, dryRun bool, syncPaths []string) error {
	// Get preview of what would happen
	preview, err := syncer.PreviewPull(ctx)
	if err != nil {
		return fmt.Errorf("failed to preview pull: %w", err)
	}

	// If nothing would be affected, proceed normally
	if len(preview.WouldOverwrite) == 0 && len(preview.WouldDownload) == 0 && len(preview.WouldConflict) == 0 {
		if !quiet {
			fmt.Printf("%s✓%s Already up to date\n", colorGreen, colorReset)
		}
		return nil
	}

	// Show warning
	fmt.Println()
	printWarning("Local ~/.claude already has files that would be affected:")
	fmt.Println()

	// Show files that would be overwritten
	for _, f := range preview.WouldOverwrite {
		localTime := f.LocalTime.Format("2006-01-02")
		remoteTime := f.RemoteTime.Format("2006-01-02")
		fmt.Printf("  %sOVERWRITE%s  %s\n", colorYellow, colorReset, f.Path)
		fmt.Printf("            %s(local: %s, remote: %s)%s\n", colorDim, localTime, remoteTime, colorReset)
	}

	// Show files that would be downloaded (new)
	for _, f := range preview.WouldDownload {
		fmt.Printf("  %sNEW%s        %s\n", colorGreen, colorReset, f.Path)
	}

	// Show files that would be kept
	for _, f := range preview.WouldKeep {
		fmt.Printf("  %sKEEP%s       %s %s(local newer)%s\n", colorCyan, colorReset, f.Path, colorDim, colorReset)
	}

	// Show local-only files
	for _, f := range preview.LocalOnlyFiles {
		fmt.Printf("  %sKEEP%s       %s %s(local only)%s\n", colorCyan, colorReset, f.Path, colorDim, colorReset)
	}

	// Show conflicts
	for _, f := range preview.WouldConflict {
		fmt.Printf("  %sCONFLICT%s   %s %s(both changed)%s\n", colorYellow, colorReset, f.Path, colorDim, colorReset)
	}

	fmt.Println()

	// If dry-run, stop here
	if dryRun {
		fmt.Printf("%sDry run complete. No changes were made.%s\n", colorDim, colorReset)
		fmt.Printf("%sRun 'claude-sync pull' to apply changes, or 'claude-sync pull --force' to skip this prompt.%s\n", colorDim, colorReset)
		return nil
	}

	// Ask user what to do
	prompt := &survey.Select{
		Message: "How would you like to proceed?",
		Options: []string{
			"Backup existing files, then pull (recommended)",
			"Overwrite without backup",
			"Abort",
		},
	}
	var choice int
	if err := survey.AskOne(prompt, &choice); err != nil {
		return err
	}

	switch choice {
	case 0:
		// Backup and proceed
		backupDir, err := createBackup(syncPaths)
		if err != nil {
			return fmt.Errorf("failed to create backup: %w", err)
		}
		printSuccess("Backup created: " + backupDir)
		fmt.Println()
		return executePull(ctx, syncer)

	case 1:
		// Proceed without backup
		fmt.Println()
		return executePull(ctx, syncer)

	default:
		// Abort
		fmt.Println("  Aborted.")
		return nil
	}
}

// createBackup creates a backup of the current ~/.claude directory
func createBackup(syncPaths []string) (string, error) {
	claudeDir := config.ClaudeDir()
	timestamp := time.Now().Format("20060102-150405")
	backupDir := claudeDir + ".backup." + timestamp

	// Create backup directory
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Copy all syncable files to backup
	files, err := sync.GetLocalFiles(claudeDir, syncPaths)
	if err != nil {
		return "", fmt.Errorf("failed to list files: %w", err)
	}

	for relPath := range files {
		srcPath := filepath.Join(claudeDir, relPath)
		dstPath := filepath.Join(backupDir, relPath)

		// Ensure destination directory exists
		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0700); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", dstDir, err)
		}

		// Copy file
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", relPath, err)
		}

		if err := os.WriteFile(dstPath, data, 0600); err != nil {
			return "", fmt.Errorf("failed to write %s: %w", relPath, err)
		}
	}

	return backupDir, nil
}

// showPullPreview shows what would happen during a pull without making changes
func showPullPreview(ctx context.Context, syncer *sync.Syncer) error {
	preview, err := syncer.PreviewPull(ctx)
	if err != nil {
		return fmt.Errorf("failed to preview pull: %w", err)
	}

	// If nothing would happen
	total := len(preview.WouldDownload) + len(preview.WouldOverwrite) + len(preview.WouldConflict)
	if total == 0 {
		fmt.Printf("%s✓%s Already up to date (dry run)\n", colorGreen, colorReset)
		return nil
	}

	fmt.Println()
	fmt.Printf("%sDry run - no changes will be made:%s\n\n", colorBold, colorReset)

	// Show files that would be downloaded (new)
	if len(preview.WouldDownload) > 0 {
		fmt.Printf("Would download (%d new files):\n", len(preview.WouldDownload))
		for _, f := range preview.WouldDownload {
			fmt.Printf("  %s+%s %s (%s)\n", colorGreen, colorReset, f.Path, util.FormatSize(f.RemoteSize))
		}
		fmt.Println()
	}

	// Show files that would be overwritten
	if len(preview.WouldOverwrite) > 0 {
		fmt.Printf("Would overwrite (%d files):\n", len(preview.WouldOverwrite))
		for _, f := range preview.WouldOverwrite {
			fmt.Printf("  %s~%s %s (local: %s, remote: %s)\n",
				colorYellow, colorReset, f.Path,
				util.FormatSize(f.LocalSize), util.FormatSize(f.RemoteSize))
		}
		fmt.Println()
	}

	// Show files that would conflict
	if len(preview.WouldConflict) > 0 {
		fmt.Printf("Would create conflicts (%d files):\n", len(preview.WouldConflict))
		for _, f := range preview.WouldConflict {
			fmt.Printf("  %s!%s %s (both local and remote changed)\n", colorYellow, colorReset, f.Path)
		}
		fmt.Println()
	}

	// Show files that would be kept
	if len(preview.WouldKeep) > 0 {
		fmt.Printf("Would keep local (%d files newer locally):\n", len(preview.WouldKeep))
		for _, f := range preview.WouldKeep {
			fmt.Printf("  %s=%s %s\n", colorDim, colorReset, f.Path)
		}
		fmt.Println()
	}

	// Summary
	fmt.Printf("%sSummary:%s %d would download, %d would overwrite, %d conflicts, %d unchanged\n",
		colorBold, colorReset,
		len(preview.WouldDownload),
		len(preview.WouldOverwrite),
		len(preview.WouldConflict),
		len(preview.WouldKeep))
	fmt.Println()
	fmt.Printf("%sRun 'claude-sync pull' to apply these changes.%s\n", colorDim, colorReset)

	return nil
}

// executePull performs the actual pull operation with progress output
func executePull(ctx context.Context, syncer *sync.Syncer) error {
	if !quiet {
		syncer.SetProgressFunc(func(event sync.ProgressEvent) {
			if event.Error != nil {
				fmt.Printf("\r%s✗%s %s: %v\n", colorYellow, colorReset, event.Path, event.Error)
				return
			}

			switch event.Action {
			case "scan":
				if event.Complete {
					fmt.Printf("\r%s✓%s Already up to date\n", colorGreen, colorReset)
				} else {
					fmt.Printf("%s⋯%s %s\n", colorDim, colorReset, event.Path)
				}
			case "download":
				if event.Complete {
					// Final newline after progress
				} else {
					progress := fmt.Sprintf("[%d/%d]", event.Current, event.Total)
					shortPath := util.TruncatePath(event.Path, 50)
					fmt.Printf("\r%s↓%s %s%s%s %s (%s)%s",
						colorGreen, colorReset,
						colorDim, progress, colorReset,
						shortPath, util.FormatSize(event.Size),
						strings.Repeat(" ", 10))
				}
			case "conflict":
				fmt.Printf("\r%s⚠%s Conflict: %s (saved as .conflict)\n",
					colorYellow, colorReset, event.Path)
			}
		})
	}

	result, err := syncer.Pull(ctx)
	if err != nil {
		return err
	}

	if !quiet {
		fmt.Println()

		if len(result.Downloaded) == 0 && len(result.Conflicts) == 0 && len(result.Errors) == 0 {
			// Already printed "Already up to date"
		} else {
			var parts []string
			if len(result.Downloaded) > 0 {
				parts = append(parts, fmt.Sprintf("%s%d downloaded%s", colorGreen, len(result.Downloaded), colorReset))
			}
			if len(result.Conflicts) > 0 {
				parts = append(parts, fmt.Sprintf("%s%d conflicts%s", colorYellow, len(result.Conflicts), colorReset))
			}
			if len(result.Errors) > 0 {
				parts = append(parts, fmt.Sprintf("%s%d failed%s", colorYellow, len(result.Errors), colorReset))
			}
			if len(parts) > 0 {
				fmt.Printf("%s✓%s Pull complete: %s\n", colorGreen, colorReset, strings.Join(parts, ", "))
			}

			if len(result.Conflicts) > 0 {
				fmt.Printf("\n%sConflicts (both local and remote changed):%s\n", colorYellow, colorReset)
				for _, c := range result.Conflicts {
					fmt.Printf("  %s•%s %s\n", colorYellow, colorReset, c)
				}
				fmt.Printf("\n%sLocal versions kept. Remote saved as .conflict files.%s\n", colorDim, colorReset)
				fmt.Printf("%sRun '%sclaude-sync conflicts%s%s' to review and resolve.%s\n", colorDim, colorCyan, colorReset, colorDim, colorReset)
			}

			if len(result.Errors) > 0 {
				fmt.Printf("\n%sErrors:%s\n", colorYellow, colorReset)
				for _, e := range result.Errors {
					fmt.Printf("  %s•%s %v\n", colorYellow, colorReset, e)
				}
			}
		}
	}

	return nil
}

func changelogCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Show release history and changelog",
		Long: `Display the release history of claude-sync with version notes.

Examples:
  claude-sync changelog          # Show recent releases
  claude-sync changelog --limit 5 # Show last 5 releases`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s⋯%s Fetching releases...\n\n", colorDim, colorReset)

			releases, err := getAllReleases(limit)
			if err != nil {
				return fmt.Errorf("failed to fetch changelog: %w", err)
			}

			if len(releases) == 0 {
				fmt.Printf("%sNo releases found.%s\n", colorDim, colorReset)
				return nil
			}

			// Header
			fmt.Printf("%s╭─────────────────────────────────────────────────────────────╮%s\n", colorCyan, colorReset)
			fmt.Printf("%s│%s  %sCLAUDE-SYNC CHANGELOG%s                                      %s│%s\n", colorCyan, colorReset, colorBold, colorReset, colorCyan, colorReset)
			fmt.Printf("%s╰─────────────────────────────────────────────────────────────╯%s\n\n", colorCyan, colorReset)

			currentVersion := strings.TrimPrefix(version, "v")

			for i, release := range releases {
				tagVersion := strings.TrimPrefix(release.TagName, "v")
				isCurrent := tagVersion == currentVersion

				// Version header with current marker
				if isCurrent {
					fmt.Printf("%s▸ %s%s %s(current)%s\n", colorGreen, release.TagName, colorReset, colorGreen, colorReset)
				} else {
					fmt.Printf("%s▸ %s%s\n", colorCyan, release.TagName, colorReset)
				}

				// Release date
				if release.PublishedAt != "" {
					if t, err := time.Parse(time.RFC3339, release.PublishedAt); err == nil {
						fmt.Printf("  %sReleased: %s%s\n", colorDim, t.Format("January 2, 2006"), colorReset)
					}
				}

				// Release notes (body)
				if release.Body != "" {
					fmt.Println()
					printReleaseBody(release.Body)
				}

				// Separator between releases
				if i < len(releases)-1 {
					fmt.Printf("\n%s───────────────────────────────────────────────────────────────%s\n\n", colorDim, colorReset)
				}
			}

			fmt.Println()
			fmt.Printf("%sView all releases: %shttps://github.com/tawanorg/claude-sync/releases%s\n", colorDim, colorCyan, colorReset)
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "Number of releases to show")

	return cmd
}

// GitHubReleaseWithBody extends GitHubRelease with body and published date
type GitHubReleaseWithBody struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func getAllReleases(limit int) ([]GitHubReleaseWithBody, error) {
	url := fmt.Sprintf("https://api.github.com/repos/tawanorg/claude-sync/releases?per_page=%d", limit)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "claude-sync/"+version)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var releases []GitHubReleaseWithBody
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

func printReleaseBody(body string) {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format markdown-style headers
		if strings.HasPrefix(line, "## ") {
			fmt.Printf("  %s%s%s\n", colorBold, strings.TrimPrefix(line, "## "), colorReset)
			continue
		}
		if strings.HasPrefix(line, "### ") {
			fmt.Printf("  %s%s%s\n", colorBold, strings.TrimPrefix(line, "### "), colorReset)
			continue
		}

		// Format bullet points
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			content := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			fmt.Printf("  %s•%s %s\n", colorCyan, colorReset, content)
			continue
		}

		// Regular text
		fmt.Printf("  %s\n", line)
	}
}

// MCP sync commands

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP server sync",
		Long: `Sync global MCP server configurations from ~/.claude.json across devices.`,
	}
	cmd.AddCommand(
		mcpStatusCmd(),
		mcpListCmd(),
		mcpPushCmd(),
		mcpPullCmd(),
		mcpEnableCmd(),
		mcpDisableCmd(),
	)
	return cmd
}

func mcpStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show MCP sync settings and local server state",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Auto-sync setting
			autoSync := cfg.MCPSync != nil && *cfg.MCPSync
			if autoSync {
				fmt.Printf("  Auto-sync  %s✓ enabled%s  (included in every push/pull)\n", colorGreen, colorReset)
			} else {
				fmt.Printf("  Auto-sync  %s✗ disabled%s  (use --include-mcp or 'mcp push/pull')\n", colorDim, colorReset)
			}

			// Local server count + pending changes
			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}
			ctx := context.Background()
			status, err := syncer.MCPStatus(ctx)
			if err != nil {
				return err
			}

			if status.ServerCount == 0 {
				fmt.Printf("  Servers    %s0 servers%s in %s\n", colorDim, colorReset, config.ClaudeJSONPath())
			} else {
				fmt.Printf("  Servers    %d configured\n", status.ServerCount)
			}

			if status.HasChanges {
				fmt.Printf("  Changes    %s● unpushed local changes%s\n", colorYellow, colorReset)
			} else {
				fmt.Printf("  Changes    %s✓ in sync%s\n", colorGreen, colorReset)
			}

			fmt.Printf("\n  %smcp enable%s   — auto-include in every push/pull\n", colorDim, colorReset)
			fmt.Printf("  %smcp disable%s  — manual only\n", colorDim, colorReset)

			return nil
		},
	}
}

func mcpListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List local MCP server configurations",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()
			status, err := syncer.MCPStatus(ctx)
			if err != nil {
				return err
			}

			if status.ServerCount == 0 {
				fmt.Printf("%s⋯%s No MCP servers found in %s\n", colorDim, colorReset, config.ClaudeJSONPath())
				return nil
			}

			fmt.Printf("%sMCP Servers%s (%d total):\n\n", colorBold, colorReset, status.ServerCount)
			for name := range status.Servers {
				syncMark := fmt.Sprintf("%s●%s", colorGreen, colorReset)
				if status.HasChanges {
					syncMark = fmt.Sprintf("%s○%s", colorYellow, colorReset)
				}
				fmt.Printf("  %s %s\n", syncMark, name)
			}

			if status.HasChanges {
				fmt.Printf("\n%s○ = has local changes not yet pushed%s\n", colorDim, colorReset)
			}

			return nil
		},
	}
}

func mcpPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push MCP server configs to cloud storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()
			return runMCPPush(ctx, syncer)
		},
	}
}

func mcpPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull MCP server configs from cloud storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}

			ctx := context.Background()
			return runMCPPull(ctx, syncer)
		},
	}
}

func mcpEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable automatic MCP sync on every push/pull (syncs now too)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			t := true
			cfg.MCPSync = &t
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s MCP auto-sync enabled.\n", colorGreen, colorReset)

			syncer, err := sync.NewSyncer(cfg, quiet)
			if err != nil {
				return err
			}
			ctx := context.Background()
			return runMCPPush(ctx, syncer)
		},
	}
}

func mcpDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable automatic MCP sync on every push/pull",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			f := false
			cfg.MCPSync = &f
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s MCP sync disabled. Use --include-mcp flag for one-time sync.\n", colorGreen, colorReset)
			return nil
		},
	}
}

func runMCPPush(ctx context.Context, syncer *sync.Syncer) error {
	result, err := syncer.PushMCP(ctx)
	if err != nil {
		return fmt.Errorf("MCP push failed: %w", err)
	}

	if !quiet {
		if result.Unchanged {
			fmt.Printf("%s✓%s MCP servers: no changes to push\n", colorGreen, colorReset)
		} else {
			fmt.Printf("%s✓%s MCP servers: %s%d servers pushed%s\n",
				colorGreen, colorReset, colorGreen, result.ServersPushed, colorReset)
		}
	}
	return nil
}

func pathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Manage sync paths and sub-path filters",
		Long: `Manage which paths under ~/.claude/ are synced to cloud storage.

HOW IT WORKS
  Effective sync = sync_list − exclude_list

  sync_list    built-in defaults + custom additions
  exclude_list sub-path glob filters (block files inside a synced dir)

  A path not in sync_list is never synced — no exclude needed.
  A path in sync_list but matched by an exclude glob is skipped.

REMOVING A DEFAULT PATH
  Use 'paths remove'. It removes the path from sync_list AND adds it
  to exclude so 'paths reset' never silently re-includes it.
  Use 'paths add' to reverse this.

EXCLUDE IS FOR SUB-PATH FILTERS ONLY
  Use 'paths exclude' only to skip files or globs inside an
  already-synced directory. Do not use it to remove a top-level path.

    plugins/**/node_modules/**   skip node_modules inside plugins/
    projects/my-app/**.secret    skip .secret files in one project

  Glob syntax:
    file.txt    exact relative path
    dir/*       direct children of a directory
    dir/**      all files recursively under a directory
    **/*.ext    any matching file anywhere under a sync path

SUBCOMMANDS
  list                  Active defaults, removed defaults, custom, filters
  add <path>            Add custom path or re-enable a removed default
  remove <path>         Smart: default→excluded, custom→deleted  [-f]
  exclude <glob>        Add a sub-path glob filter
  unexclude <glob>      Remove a sub-path glob filter
  edit                  Interactive multi-select for sync paths
  reset                 Restore defaults, clear custom paths and filters [-f]

EXAMPLES
  # See full sync state
  claude-sync paths

  # Stop syncing history (stays off after reset too)
  claude-sync paths remove history.jsonl

  # Re-enable history
  claude-sync paths add history.jsonl

  # Add a custom directory
  claude-sync paths add my-notes

  # Skip node_modules inside plugins/
  claude-sync paths exclude "plugins/**/node_modules/**"

  # Remove that filter
  claude-sync paths unexclude "plugins/**/node_modules/**"

  # Reset to factory defaults
  claude-sync paths reset`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPathsList()
		},
	}
	cmd.AddCommand(
		pathsListCmd(),
		pathsAddCmd(),
		pathsRemoveCmd(),
		pathsEditCmd(),
		pathsResetCmd(),
		pathsExcludeCmd(),
		pathsUnexcludeCmd(),
	)
	return cmd
}

func pathsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "Show sync state: active, removed defaults, custom, filters",
		Example: "  claude-sync paths list",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPathsList()
		},
	}
}

func runPathsList() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	source := "default"
	if len(cfg.SyncPaths) > 0 {
		source = "config.yaml"
	}

	fmt.Printf("\n%sSync Paths%s (%s):\n", colorBold, colorReset, source)
	for _, p := range cfg.GetEffectiveSyncPaths() {
		fmt.Printf("  %s+%s %s\n", colorGreen, colorReset, p)
	}

	if len(cfg.Exclude) > 0 {
		fmt.Printf("\n%sExclude%s:\n", colorBold, colorReset)
		for _, p := range cfg.Exclude {
			fmt.Printf("  %s-%s %s\n", colorYellow, colorReset, p)
		}
	}

	fmt.Println()
	return nil
}

func pathsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <path>",
		Short: "Add a path to sync, or re-enable a removed default",
		Long: `Add a relative path under ~/.claude/ to the sync list.

If the path was previously removed (and added to exclude), the
conflicting exclude entry is automatically cleaned up so the path
is actually synced again.

The path does not have to exist yet — it is picked up on the next push.`,
		Example: `  claude-sync paths add my-notes
  claude-sync paths add history.jsonl`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			newPath := args[0]

			effective := cfg.GetEffectiveSyncPaths()
			for _, p := range effective {
				if p == newPath {
					fmt.Printf("%s!%s %s is already in the sync list\n", colorYellow, colorReset, newPath)
					return nil
				}
			}

			claudeDir := config.ClaudeDir()
			if _, err := os.Stat(filepath.Join(claudeDir, newPath)); os.IsNotExist(err) {
				fmt.Printf("%s!%s %s does not exist under ~/.claude (adding anyway)\n", colorYellow, colorReset, newPath)
			}

			cfg.SyncPaths = append(effective, newPath)

			// Remove any exclude patterns that would block this path
			filtered := cfg.Exclude[:0:0]
			removed := 0
			for _, e := range cfg.Exclude {
				base := strings.TrimSuffix(strings.TrimSuffix(e, "/**"), "/*")
				if base == newPath || e == newPath {
					removed++
					continue
				}
				filtered = append(filtered, e)
			}
			cfg.Exclude = filtered

			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Added %s to sync\n", colorGreen, colorReset, newPath)
			if removed > 0 {
				fmt.Printf("%s✓%s Removed %d conflicting exclude(s) for %s\n", colorGreen, colorReset, removed, newPath)
			}
			return nil
		},
	}
}

func pathsRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <path>",
		Short: "Remove a path from sync (smart: default→excluded, custom→deleted)",
		Long: `Remove a path from the sync list.

Default paths: removed from sync AND added to the exclude list so
  that 'paths reset' never silently re-includes them.
Custom paths:  simply deleted from the sync list — no exclude needed
  since they were never part of the defaults.

Use 'paths add <path>' to reverse a removal.
Use --force (-f) to skip the confirmation prompt.`,
		Example: `  claude-sync paths remove history.jsonl
  claude-sync paths remove my-notes --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			target := args[0]
			effective := cfg.GetEffectiveSyncPaths()

			found := false
			for _, p := range effective {
				if p == target {
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("%s!%s %s is not in the sync list\n", colorYellow, colorReset, target)
				return nil
			}

			if !force {
				var confirm bool
				prompt := &survey.Confirm{
					Message: fmt.Sprintf("Remove %q from sync?", target),
					Default: false,
				}
				if err := survey.AskOne(prompt, &confirm); err != nil || !confirm {
					fmt.Println("  Cancelled.")
					return nil
				}
			}

			newPaths := make([]string, 0, len(effective)-1)
			for _, p := range effective {
				if p != target {
					newPaths = append(newPaths, p)
				}
			}
			cfg.SyncPaths = newPaths

			// Only add to exclude if it's a default path (to survive reset)
			defaultSet := make(map[string]struct{}, len(config.SyncPaths))
			for _, p := range config.SyncPaths {
				defaultSet[p] = struct{}{}
			}

			var addedExclude string
			if _, isDefault := defaultSet[target]; isDefault {
				claudeDir := config.ClaudeDir()
				excludePattern := target
				if fi, err := os.Stat(filepath.Join(claudeDir, target)); err == nil && fi.IsDir() {
					excludePattern = target + "/*"
				}
				alreadyExcluded := false
				for _, e := range cfg.Exclude {
					if e == excludePattern {
						alreadyExcluded = true
						break
					}
				}
				if !alreadyExcluded {
					cfg.Exclude = append(cfg.Exclude, excludePattern)
					addedExclude = excludePattern
				}
			}

			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Removed %s from sync\n", colorGreen, colorReset, target)
			if addedExclude != "" {
				fmt.Printf("%s✓%s Added exclude filter %s (survives reset)\n", colorGreen, colorReset, addedExclude)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation")
	return cmd
}

func pathsEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Interactive multi-select editor for sync paths",
		Long: `Open an interactive list of all current sync paths.

Space toggles a path; Enter saves. To permanently remove a default
path (so it stays off after reset), use 'paths remove' instead.`,
		Example: "  claude-sync paths edit",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			effective := cfg.GetEffectiveSyncPaths()

			var selected []string
			prompt := &survey.MultiSelect{
				Message: "Select paths to sync (space to toggle, enter to save):",
				Options: effective,
				Default: effective,
			}
			if err := survey.AskOne(prompt, &selected); err != nil {
				return err
			}

			cfg.SyncPaths = selected
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Sync paths updated (%d paths)\n", colorGreen, colorReset, len(selected))
			return nil
		},
	}
}

func pathsResetCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Restore factory defaults, clear custom paths and filters",
		Long: `Clear all custom sync paths, removed-default markers, and sub-path
filters, restoring the built-in default set.

Default sync paths:
  CLAUDE.md, settings.json, settings.local.json, agents, commands,
  skills, plugins, projects, plans, tasks, history.jsonl, rules,
  workflows

Use --force (-f) to skip the confirmation prompt.`,
		Example: `  claude-sync paths reset
  claude-sync paths reset --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if !force {
				var confirm bool
				prompt := &survey.Confirm{
					Message: "Reset all sync paths and filters to factory defaults?",
					Default: false,
				}
				if err := survey.AskOne(prompt, &confirm); err != nil || !confirm {
					fmt.Println("  Cancelled.")
					return nil
				}
			}

			cfg.SyncPaths = nil
			cfg.Exclude = nil
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Sync paths reset to defaults\n", colorGreen, colorReset)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation")
	return cmd
}

func pathsExcludeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exclude <glob>",
		Short: "Add a sub-path glob filter inside a synced directory",
		Long: `Add a glob pattern that blocks specific files or subdirectories
inside an already-synced path.

Use this for filtering within a sync path — NOT for removing a top-level
sync path. To stop syncing a whole path use 'paths remove'.

Glob syntax:
  dir/*              direct children of a directory
  dir/**             all files recursively under a directory
  **/*.ext           any matching file anywhere under a sync path
  path/to/file.txt   exact relative file path inside a synced dir

To remove a filter, use: paths unexclude <glob>`,
		Example: `  # Skip node_modules inside plugins/
  claude-sync paths exclude "plugins/**/node_modules/**"

  # Skip .secret files in one project
  claude-sync paths exclude "projects/my-app/**.secret"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			pattern := args[0]

			// Guard: if pattern exactly matches an active sync path, guide to 'remove'
			for _, p := range cfg.GetEffectiveSyncPaths() {
				if p == pattern {
					fmt.Printf("%s!%s %q is a top-level sync path — use 'paths remove %s' instead\n",
						colorYellow, colorReset, pattern, pattern)
					return nil
				}
			}

			for _, p := range cfg.Exclude {
				if p == pattern {
					fmt.Printf("%s!%s %s is already in the filter list\n", colorYellow, colorReset, pattern)
					return nil
				}
			}
			cfg.Exclude = append(cfg.Exclude, pattern)
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Added sub-path filter: %s\n", colorGreen, colorReset, pattern)
			return nil
		},
	}
}

func pathsUnexcludeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unexclude <glob>",
		Short: "Remove a sub-path glob filter",
		Long: `Remove a glob pattern from the sub-path filter list.

If the pattern was added automatically by 'paths remove' (to mark a
removed default), use 'paths add <path>' instead — it handles both
re-adding the path to sync and removing the filter in one step.`,
		Example: `  claude-sync paths unexclude "plugins/**/node_modules/**"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			pattern := args[0]
			newExclude := make([]string, 0, len(cfg.Exclude))
			found := false
			for _, p := range cfg.Exclude {
				if p == pattern {
					found = true
					continue
				}
				newExclude = append(newExclude, p)
			}
			if !found {
				fmt.Printf("%s!%s %s not found in filter list\n", colorYellow, colorReset, pattern)
				return nil
			}
			cfg.Exclude = newExclude
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Printf("%s✓%s Removed filter: %s\n", colorGreen, colorReset, pattern)
			return nil
		},
	}
}

func runMCPPull(ctx context.Context, syncer *sync.Syncer) error {
	result, err := syncer.PullMCP(ctx)
	if err != nil {
		return fmt.Errorf("MCP pull failed: %w", err)
	}

	if !quiet {
		if result.NoRemote {
			fmt.Printf("%s⋯%s MCP servers: no remote data found\n", colorDim, colorReset)
			return nil
		}

		var parts []string
		if len(result.Added) > 0 {
			parts = append(parts, fmt.Sprintf("%s%d added%s", colorGreen, len(result.Added), colorReset))
			for _, name := range result.Added {
				fmt.Printf("  %s+%s %s\n", colorGreen, colorReset, name)
			}
		}
		if len(result.Updated) > 0 {
			parts = append(parts, fmt.Sprintf("%s%d updated%s", colorCyan, len(result.Updated), colorReset))
			for _, name := range result.Updated {
				fmt.Printf("  %s~%s %s\n", colorCyan, colorReset, name)
			}
		}
		if len(result.Kept) > 0 {
			parts = append(parts, fmt.Sprintf("%d unchanged", len(result.Kept)))
		}
		if len(result.Conflicts) > 0 {
			parts = append(parts, fmt.Sprintf("%s%d conflicts%s", colorYellow, len(result.Conflicts), colorReset))
			for _, c := range result.Conflicts {
				fmt.Printf("  %s!%s %s (kept local version)\n", colorYellow, colorReset, c.Key)
			}
		}

		if len(parts) > 0 {
			fmt.Printf("%s✓%s MCP pull complete: %s\n", colorGreen, colorReset, strings.Join(parts, ", "))
		} else {
			fmt.Printf("%s✓%s MCP servers: already up to date\n", colorGreen, colorReset)
		}
	}
	return nil
}
