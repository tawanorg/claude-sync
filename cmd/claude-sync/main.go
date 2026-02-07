package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tawanorg/claude-sync/internal/config"
	"github.com/tawanorg/claude-sync/internal/crypto"
	"github.com/tawanorg/claude-sync/internal/storage"
	"github.com/tawanorg/claude-sync/internal/sync"
)

var (
	version = "0.1.0"
	quiet   bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "claude-sync",
		Short:   "Sync Claude Code sessions across devices",
		Long:    `A CLI tool to sync your ~/.claude directory across devices using Cloudflare R2 with encryption.`,
		Version: version,
	}

	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output")

	rootCmd.AddCommand(
		initCmd(),
		pushCmd(),
		pullCmd(),
		statusCmd(),
		diffCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	var accountID, accessKey, secretKey, bucket string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize claude-sync configuration",
		Long:  `Set up Cloudflare R2 credentials and generate encryption keys.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.Exists() {
				fmt.Println("Configuration already exists at", config.ConfigFilePath())
				fmt.Print("Overwrite? [y/N]: ")
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			// Interactive prompts if not provided via flags
			reader := bufio.NewReader(os.Stdin)

			if accountID == "" {
				fmt.Print("Cloudflare Account ID: ")
				accountID, _ = reader.ReadString('\n')
				accountID = strings.TrimSpace(accountID)
			}

			if accessKey == "" {
				fmt.Print("R2 Access Key ID: ")
				accessKey, _ = reader.ReadString('\n')
				accessKey = strings.TrimSpace(accessKey)
			}

			if secretKey == "" {
				fmt.Print("R2 Secret Access Key: ")
				secretKey, _ = reader.ReadString('\n')
				secretKey = strings.TrimSpace(secretKey)
			}

			if bucket == "" {
				fmt.Print("R2 Bucket Name [claude-sync]: ")
				bucket, _ = reader.ReadString('\n')
				bucket = strings.TrimSpace(bucket)
				if bucket == "" {
					bucket = "claude-sync"
				}
			}

			// Create config directory
			configDir := config.ConfigDirPath()
			if err := os.MkdirAll(configDir, 0700); err != nil {
				return fmt.Errorf("failed to create config directory: %w", err)
			}

			// Generate age key if not exists
			keyPath := config.AgeKeyFilePath()
			if !crypto.KeyExists(keyPath) {
				fmt.Println("Generating encryption key...")
				if err := crypto.GenerateKey(keyPath); err != nil {
					return fmt.Errorf("failed to generate encryption key: %w", err)
				}
				fmt.Printf("Encryption key saved to: %s\n", keyPath)
				fmt.Println("IMPORTANT: Back up this key file! You'll need it on other devices.")
			} else {
				fmt.Printf("Using existing encryption key: %s\n", keyPath)
			}

			// Save config
			cfg := &config.Config{
				AccountID:       accountID,
				AccessKeyID:     accessKey,
				SecretAccessKey: secretKey,
				Bucket:          bucket,
				EncryptionKey:   "~/.claude-sync/age-key.txt",
			}

			if err := config.Save(cfg); err != nil {
				return err
			}

			fmt.Printf("Configuration saved to: %s\n", config.ConfigFilePath())

			// Test connection
			fmt.Println("\nTesting R2 connection...")
			cfg.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
			r2, err := storage.NewR2Client(cfg)
			if err != nil {
				return fmt.Errorf("failed to create R2 client: %w", err)
			}

			ctx := context.Background()
			exists, err := r2.BucketExists(ctx)
			if err != nil {
				fmt.Printf("Warning: Could not check bucket: %v\n", err)
			} else if exists {
				fmt.Println("Bucket exists and is accessible!")
			} else {
				fmt.Printf("Bucket '%s' not found. Create it in the Cloudflare dashboard.\n", bucket)
			}

			fmt.Println("\nSetup complete! You can now use:")
			fmt.Println("  claude-sync push   - Upload your sessions")
			fmt.Println("  claude-sync pull   - Download sessions from cloud")
			fmt.Println("  claude-sync status - Show pending changes")

			return nil
		},
	}

	cmd.Flags().StringVar(&accountID, "account-id", "", "Cloudflare Account ID")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "R2 Access Key ID")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "R2 Secret Access Key")
	cmd.Flags().StringVar(&bucket, "bucket", "", "R2 Bucket Name")

	return cmd
}

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Upload local changes to R2",
		Long:  `Encrypt and upload changed files from ~/.claude to Cloudflare R2.`,
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
			result, err := syncer.Push(ctx)
			if err != nil {
				return err
			}

			if !quiet {
				if len(result.Uploaded) > 0 {
					fmt.Printf("\nUploaded %d file(s)\n", len(result.Uploaded))
				}
				if len(result.Deleted) > 0 {
					fmt.Printf("Deleted %d file(s)\n", len(result.Deleted))
				}
				if len(result.Errors) > 0 {
					fmt.Printf("\n%d error(s):\n", len(result.Errors))
					for _, e := range result.Errors {
						fmt.Printf("  - %v\n", e)
					}
				}
			}

			return nil
		},
	}
}

func pullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Download remote changes from R2",
		Long:  `Download and decrypt changed files from Cloudflare R2 to ~/.claude.`,
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
			result, err := syncer.Pull(ctx)
			if err != nil {
				return err
			}

			if !quiet {
				if len(result.Downloaded) > 0 {
					fmt.Printf("\nDownloaded %d file(s)\n", len(result.Downloaded))
				}
				if len(result.Conflicts) > 0 {
					fmt.Printf("\n%d conflict(s) (saved as .conflict files):\n", len(result.Conflicts))
					for _, c := range result.Conflicts {
						fmt.Printf("  - %s\n", c)
					}
				}
				if len(result.Errors) > 0 {
					fmt.Printf("\n%d error(s):\n", len(result.Errors))
					for _, e := range result.Errors {
						fmt.Printf("  - %v\n", e)
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
		Long:  `Compare local ~/.claude with remote R2 storage.`,
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
