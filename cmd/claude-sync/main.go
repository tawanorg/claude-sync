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
	fmt.Println()
	fmt.Printf("  %sEncrypted with age • R2 / S3 / GCS supported%s\n", colorDim, colorReset)
	fmt.Printf("  %sBuilt with ♥ by @tawanorg%s\n", colorDim, colorReset)
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
	var usePassphrase bool

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

Use --passphrase to derive the key from a memorable passphrase - same
passphrase on any device produces the same key, no file copying needed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Show banner
			printBanner()

			if config.Exists() {
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

			keyPath := config.AgeKeyFilePath()

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

			if usePassphrase {
				if crypto.KeyExists(keyPath) {
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
				var passphrase string
				for {
					prompt := &survey.Password{
						Message: "Passphrase (min 8 chars):",
					}
					if err := survey.AskOne(prompt, &passphrase); err != nil {
						return err
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
						return err
					}

					if passphrase != confirm {
						printWarning("Passphrases don't match.")
						continue
					}
					break
				}

				if err := crypto.GenerateKeyFromPassphrase(keyPath, passphrase); err != nil {
					return fmt.Errorf("failed to generate key: %w", err)
				}
				printSuccess("Key derived from passphrase")

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

			store, err := storage.New(storageCfg)
			if err != nil {
				// Provide helpful error messages for common issues
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

			ctx := context.Background()
			exists, err := store.BucketExists(ctx)
			if err != nil {
				printWarning("Could not verify bucket: " + err.Error())
			} else if exists {
				printSuccess("Connected to '" + storageCfg.Bucket + "'")
			} else {
				printWarning("Bucket '" + storageCfg.Bucket + "' not found")
			}

			// Save config only after successful connection test
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
		},
	}

	// Provider selection
	cmd.Flags().StringVar(&provider, "provider", "", "Storage provider: r2, s3, or gcs")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Bucket name")
	cmd.Flags().BoolVar(&usePassphrase, "passphrase", false, "Derive encryption key from passphrase")

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
	return &cobra.Command{
		Use:   "pull",
		Short: "Download remote changes from cloud storage",
		Long:  `Download and decrypt changed files from cloud storage to ~/.claude.`,
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

			ctx := context.Background()
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
