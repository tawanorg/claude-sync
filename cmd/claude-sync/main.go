package main

import (
	"bufio"
	"context"
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

	// Register storage adapters
	_ "github.com/tawanorg/claude-sync/internal/storage/gcs"
	_ "github.com/tawanorg/claude-sync/internal/storage/r2"
	_ "github.com/tawanorg/claude-sync/internal/storage/s3"
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
		updateCmd(),
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
	var usePassphrase, force bool

	// R2 flags
	var accountID, accessKey, secretKey string

	// S3 flags
	var s3Region string

	// GCS flags
	var gcsProjectID, gcsCredentialsFile string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize claude-sync configuration",
		Long: `Set up cloud storage credentials and generate encryption keys.

Supported providers:
  - r2:  Cloudflare R2 (S3-compatible, free tier: 10GB)
  - s3:  Amazon S3
  - gcs: Google Cloud Storage

Examples:
  claude-sync init                # Full setup wizard
  claude-sync init --passphrase   # Re-enter passphrase only (keeps storage config)
  claude-sync init --force        # Reset everything, start fresh`,
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
			return initFullSetup(ctx, keyPath, provider, bucket, accountID, accessKey, secretKey, s3Region, gcsProjectID, gcsCredentialsFile, usePassphrase, force)
		},
	}

	// Provider selection
	cmd.Flags().StringVar(&provider, "provider", "", "Storage provider: r2, s3, or gcs")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Bucket name")
	cmd.Flags().BoolVar(&usePassphrase, "passphrase", false, "Derive encryption key from passphrase")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing config/key without prompting")

	// R2 flags
	cmd.Flags().StringVar(&accountID, "account-id", "", "Cloudflare Account ID (R2)")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "Access Key ID (R2/S3)")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "Secret Access Key (R2/S3)")

	// S3 flags
	cmd.Flags().StringVar(&s3Region, "region", "", "AWS Region (S3)")

	// GCS flags
	cmd.Flags().StringVar(&gcsProjectID, "project-id", "", "GCP Project ID (GCS)")
	cmd.Flags().StringVar(&gcsCredentialsFile, "credentials-file", "", "Path to GCS credentials JSON file")

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

// initFullSetup handles the full init wizard
func initFullSetup(ctx context.Context, keyPath, provider, bucket, accountID, accessKey, secretKey, s3Region, gcsProjectID, gcsCredentialsFile string, usePassphrase, force bool) error {
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
	printStep(1, 3, "Select Storage Provider")
	fmt.Println()

	if provider == "" {
		prompt := &survey.Select{
			Message: "Choose your cloud storage provider:",
			Options: []string{
				"Cloudflare R2 (recommended - free tier: 10GB)",
				"Amazon S3",
				"Google Cloud Storage",
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
	printStep(2, 3, "Set Up Encryption")
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
	printStep(3, 3, "Test Connection")

	exists, err := store.BucketExists(ctx)
	if err != nil {
		printWarning("Could not verify bucket: " + err.Error())
	} else if exists {
		printSuccess("Connected to '" + storageCfg.Bucket + "'")
	} else {
		printWarning("Bucket '" + storageCfg.Bucket + "' not found")
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

	// Save config
	cfg := &config.Config{
		Storage:       storageCfg,
		EncryptionKey: "~/.claude-sync/age-key.txt",
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

func pushCmd() *cobra.Command {
	return &cobra.Command{
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
							shortPath := truncatePath(event.Path, 50)
							fmt.Printf("\r%s↑%s %s%s%s %s (%s)%s",
								colorCyan, colorReset,
								colorDim, progress, colorReset,
								shortPath, formatSize(event.Size),
								strings.Repeat(" ", 10))
						}
					case "delete":
						shortPath := truncatePath(event.Path, 50)
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

			return nil
		},
	}
}

func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}

func pullCmd() *cobra.Command {
	var dryRun, force bool

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
				hasExisting, err := hasExistingClaudeFiles()
				if err != nil {
					return err
				}

				if hasExisting && !force {
					return handleFirstPullWithExistingFiles(ctx, syncer, dryRun)
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
							shortPath := truncatePath(event.Path, 50)
							fmt.Printf("\r%s↓%s %s%s%s %s (%s)%s",
								colorGreen, colorReset,
								colorDim, progress, colorReset,
								shortPath, formatSize(event.Size),
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

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be changed without making changes")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing files without confirmation")

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
					fmt.Printf("  + %s (%s)\n", c.Path, formatSize(c.LocalSize))
				}
				fmt.Println()
			}

			if len(modified) > 0 {
				fmt.Println("Modified files:")
				for _, c := range modified {
					fmt.Printf("  ~ %s (%s)\n", c.Path, formatSize(c.LocalSize))
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
					fmt.Printf("  + %s (%s)\n", e.Path, formatSize(e.LocalSize))
				}
				fmt.Println()
			}

			if len(remoteOnly) > 0 {
				fmt.Printf("Remote only (%d files):\n", len(remoteOnly))
				for _, e := range remoteOnly {
					fmt.Printf("  - %s (%s)\n", e.Path, formatSize(e.RemoteSize))
				}
				fmt.Println()
			}

			if len(modified) > 0 {
				fmt.Printf("Modified (%d files):\n", len(modified))
				for _, e := range modified {
					fmt.Printf("  ~ %s (local: %s, remote: %s)\n", e.Path, formatSize(e.LocalSize), formatSize(e.RemoteSize))
				}
				fmt.Println()
			}

			fmt.Printf("Summary: %d synced, %d local only, %d remote only, %d modified\n",
				len(synced), len(localOnly), len(remoteOnly), len(modified))

			return nil
		},
	}
}

func formatSize(size int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
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

			// Batch resolve mode
			if resolveAll != "" {
				return batchResolveConflicts(conflicts, resolveAll)
			}

			// Interactive mode
			return interactiveResolveConflicts(conflicts, claudeDir)
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

func batchResolveConflicts(conflicts []conflictFile, keep string) error {
	keep = strings.ToLower(keep)
	if keep != "local" && keep != "remote" {
		return fmt.Errorf("--keep must be 'local' or 'remote'")
	}

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
	}

	fmt.Printf("\n%s✓%s Resolved %d conflict(s)\n", colorGreen, colorReset, len(conflicts))
	return nil
}

func interactiveResolveConflicts(conflicts []conflictFile, claudeDir string) error {
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
			formatSize(localSize), formatSize(conflictSize), c.Timestamp)

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
					resolved++
				}
				break promptLoop

			case "r", "remote":
				// Replace local with conflict
				if err := os.Rename(c.ConflictPath, c.OriginalPath); err != nil {
					fmt.Printf("        %s✗%s Error: %v\n", colorYellow, colorReset, err)
				} else {
					fmt.Printf("        %s✓%s Replaced with remote version\n\n", colorGreen, colorReset)
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
				fmt.Printf("\n%s✓%s Resolved %d of %d conflict(s)\n", colorGreen, colorReset, resolved, len(conflicts))
				return nil

			default:
				fmt.Printf("        %sInvalid choice. Use l/r/d/s/q%s\n", colorDim, colorReset)
			}
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
				fmt.Printf("        Local:  %s (%s)\n", localPath, formatSize(localInfo.Size()))
				fmt.Printf("        Remote: %s (%s)\n", conflictPath, formatSize(conflictInfo.Size()))
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
			if compareVersions(currentVersion, latestVersion) >= 0 {
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
			assetName := getBinaryName(latestVersion)
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

func getBinaryName(version string) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	return fmt.Sprintf("claude-sync-%s-%s", goos, goarch)
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

func compareVersions(v1, v2 string) int {
	// Simple semver comparison
	// Returns -1 if v1 < v2, 0 if equal, 1 if v1 > v2
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	for i := 0; i < 3; i++ {
		var p1, p2 int
		if i < len(parts1) {
			_, _ = fmt.Sscanf(parts1[i], "%d", &p1)
		}
		if i < len(parts2) {
			_, _ = fmt.Sscanf(parts2[i], "%d", &p2)
		}

		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}

	return 0
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
func hasExistingClaudeFiles() (bool, error) {
	claudeDir := config.ClaudeDir()
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		return false, nil
	}

	files, err := sync.GetLocalFiles(claudeDir, config.SyncPaths)
	if err != nil {
		return false, err
	}

	return len(files) > 0, nil
}

// handleFirstPullWithExistingFiles handles the case where the user is pulling
// for the first time but already has local files that could be overwritten
func handleFirstPullWithExistingFiles(ctx context.Context, syncer *sync.Syncer, dryRun bool) error {
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
		backupDir, err := createBackup()
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
func createBackup() (string, error) {
	claudeDir := config.ClaudeDir()
	timestamp := time.Now().Format("20060102-150405")
	backupDir := claudeDir + ".backup." + timestamp

	// Create backup directory
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Copy all syncable files to backup
	files, err := sync.GetLocalFiles(claudeDir, config.SyncPaths)
	if err != nil {
		return "", fmt.Errorf("failed to list files: %w", err)
	}

	for relPath := range files {
		srcPath := filepath.Join(claudeDir, relPath)
		dstPath := filepath.Join(backupDir, relPath)

		// Ensure destination directory exists
		dstDir := filepath.Dir(dstPath)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create directory %s: %w", dstDir, err)
		}

		// Copy file
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", relPath, err)
		}

		if err := os.WriteFile(dstPath, data, 0644); err != nil {
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
			fmt.Printf("  %s+%s %s (%s)\n", colorGreen, colorReset, f.Path, formatSize(f.RemoteSize))
		}
		fmt.Println()
	}

	// Show files that would be overwritten
	if len(preview.WouldOverwrite) > 0 {
		fmt.Printf("Would overwrite (%d files):\n", len(preview.WouldOverwrite))
		for _, f := range preview.WouldOverwrite {
			fmt.Printf("  %s~%s %s (local: %s, remote: %s)\n",
				colorYellow, colorReset, f.Path,
				formatSize(f.LocalSize), formatSize(f.RemoteSize))
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
					shortPath := truncatePath(event.Path, 50)
					fmt.Printf("\r%s↓%s %s%s%s %s (%s)%s",
						colorGreen, colorReset,
						colorDim, progress, colorReset,
						shortPath, formatSize(event.Size),
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
