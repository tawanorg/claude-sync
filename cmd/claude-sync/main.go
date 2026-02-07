package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorMagenta = "\033[35m"
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
		conflictsCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func printBanner() {
	banner := `
   _____ _                 _        _____
  / ____| |               | |      / ____|
 | |    | | __ _ _   _  __| | ___ | (___  _   _ _ __   ___
 | |    | |/ _` + "`" + ` | | | |/ _` + "`" + ` |/ _ \ \___ \| | | | '_ \ / __|
 | |____| | (_| | |_| | (_| |  __/ ____) | |_| | | | | (__
  \_____|_|\__,_|\__,_|\__,_|\___||_____/ \__, |_| |_|\___|
                                           __/ |
                                          |___/
`
	fmt.Print(colorCyan + banner + colorReset)
	fmt.Printf("  %sWelcome to Claude Sync!%s %sv%s%s\n", colorBold, colorReset, colorDim, version, colorReset)
	fmt.Println()
	fmt.Printf("  %sSync your Claude Code sessions across all your devices.%s\n", colorReset, colorReset)
	fmt.Printf("  %sEnd-to-end encrypted with age • Stored on Cloudflare R2 (free tier)%s\n", colorDim, colorReset)
	fmt.Println()
	fmt.Printf("  %sBuilt with love by @tawanorg%s\n", colorDim, colorReset)
	fmt.Printf("  %sGitHub: https://github.com/tawanorg/claude-sync%s\n", colorDim, colorReset)
	fmt.Printf("  %sContributions welcome! Issues, PRs, and feedback appreciated.%s\n", colorDim, colorReset)
	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
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

func printCommand(cmd string) {
	fmt.Printf("      %s$ %s%s\n", colorMagenta, cmd, colorReset)
}

func promptInput(reader *bufio.Reader, prompt string, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("      %s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("      %s: ", prompt)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" && defaultVal != "" {
		return defaultVal
	}
	return input
}

func waitForEnter(reader *bufio.Reader) {
	fmt.Printf("\n      %sPress Enter when ready...%s", colorDim, colorReset)
	reader.ReadString('\n')
}

func initCmd() *cobra.Command {
	var accountID, accessKey, secretKey, bucket string
	var skipGuide bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize claude-sync configuration",
		Long:  `Set up Cloudflare R2 credentials and generate encryption keys.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)

			// Show banner
			printBanner()

			if config.Exists() {
				printWarning("Configuration already exists at " + config.ConfigFilePath())
				fmt.Print("      Overwrite? [y/N]: ")
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("      Aborted.")
					return nil
				}
				fmt.Println()
			}

			totalSteps := 4
			if skipGuide {
				totalSteps = 3
			}

			// Step 1: Create R2 Bucket (with guidance)
			if !skipGuide {
				printStep(1, totalSteps, "Create Cloudflare R2 Bucket")
				printInfo("R2 is Cloudflare's S3-compatible storage (free tier: 10GB)")
				fmt.Println()
				printInfo("1. Go to: https://dash.cloudflare.com/?to=/:account/r2/new")
				printInfo("2. Click 'Create bucket'")
				printInfo("3. Name it 'claude-sync' (or your preferred name)")
				printInfo("4. Leave defaults and click 'Create bucket'")
				waitForEnter(reader)
			}

			// Step 2: Get R2 API Token
			currentStep := 1
			if !skipGuide {
				currentStep = 2
			}
			printStep(currentStep, totalSteps, "Get R2 API Credentials")
			if !skipGuide {
				printInfo("You need an API token with read/write access to R2.")
				fmt.Println()
				printInfo("1. Go to: https://dash.cloudflare.com/?to=/:account/r2/api-tokens")
				printInfo("2. Click 'Create API token'")
				printInfo("3. Give it a name like 'claude-sync'")
				printInfo("4. Permissions: 'Object Read & Write'")
				printInfo("5. Specify bucket: select your bucket (or leave as 'All')")
				printInfo("6. Click 'Create API Token'")
				fmt.Println()
				printInfo("Copy the credentials shown (you won't see them again!)")
				waitForEnter(reader)
				fmt.Println()
			}

			// Prompt for Account ID
			if accountID == "" {
				printInfo("Your Account ID is in the R2 dashboard URL:")
				printInfo("https://dash.cloudflare.com/<ACCOUNT_ID>/r2/...")
				fmt.Println()
				accountID = promptInput(reader, "Account ID", "")
			}

			// Prompt for Access Key
			if accessKey == "" {
				fmt.Println()
				accessKey = promptInput(reader, "Access Key ID", "")
			}

			// Prompt for Secret Key
			if secretKey == "" {
				secretKey = promptInput(reader, "Secret Access Key", "")
			}

			// Prompt for Bucket
			if bucket == "" {
				fmt.Println()
				bucket = promptInput(reader, "Bucket name", "claude-sync")
			}

			// Step 3: Generate Encryption Key
			currentStep++
			printStep(currentStep, totalSteps, "Generate Encryption Key")
			printInfo("All files are encrypted locally before upload using 'age' encryption.")
			fmt.Println()

			configDir := config.ConfigDirPath()
			if err := os.MkdirAll(configDir, 0700); err != nil {
				return fmt.Errorf("failed to create config directory: %w", err)
			}

			keyPath := config.AgeKeyFilePath()
			if !crypto.KeyExists(keyPath) {
				if err := crypto.GenerateKey(keyPath); err != nil {
					return fmt.Errorf("failed to generate encryption key: %w", err)
				}
				printSuccess("Encryption key generated: " + keyPath)
				fmt.Println()
				printWarning("IMPORTANT: Back up this key file!")
				printInfo("You'll need it to decrypt your sessions on other devices.")
				printInfo("Without it, your synced data cannot be recovered.")
			} else {
				printSuccess("Using existing encryption key: " + keyPath)
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

			// Step 4: Test Connection
			currentStep++
			printStep(currentStep, totalSteps, "Test Connection")

			cfg.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
			r2, err := storage.NewR2Client(cfg)
			if err != nil {
				return fmt.Errorf("failed to create R2 client: %w", err)
			}

			ctx := context.Background()
			exists, err := r2.BucketExists(ctx)
			if err != nil {
				printWarning("Could not verify bucket: " + err.Error())
				printInfo("Check your credentials and try again.")
			} else if exists {
				printSuccess("Connected to R2 bucket '" + bucket + "'")
			} else {
				printWarning("Bucket '" + bucket + "' not found")
				printInfo("Create it at: https://dash.cloudflare.com/?to=/:account/r2/new")
			}

			// Success message
			fmt.Println()
			fmt.Println(colorGreen + "  Setup complete!" + colorReset)
			fmt.Println()
			fmt.Println("  " + colorBold + "Next steps:" + colorReset)
			fmt.Println()
			fmt.Printf("  %s1.%s Push your sessions to the cloud:\n", colorCyan, colorReset)
			printCommand("claude-sync push")
			fmt.Println()
			fmt.Printf("  %s2.%s On another device, install and pull:\n", colorCyan, colorReset)
			printCommand("go install github.com/tawanorg/claude-sync/cmd/claude-sync@latest")
			printCommand("claude-sync init --skip-guide")
			printCommand("# Copy ~/.claude-sync/age-key.txt from this device")
			printCommand("claude-sync pull")
			fmt.Println()
			fmt.Printf("  %s3.%s (Optional) Add to shell for auto-sync:\n", colorCyan, colorReset)
			printInfo("Add to ~/.zshrc or ~/.bashrc:")
			fmt.Println()
			fmt.Printf("      %s# Auto-sync Claude sessions%s\n", colorDim, colorReset)
			fmt.Printf("      %sif command -v claude-sync &> /dev/null; then%s\n", colorDim, colorReset)
			fmt.Printf("      %s  claude-sync pull --quiet &%s\n", colorDim, colorReset)
			fmt.Printf("      %sfi%s\n", colorDim, colorReset)
			fmt.Printf("      %strap 'claude-sync push --quiet' EXIT%s\n", colorDim, colorReset)
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&accountID, "account-id", "", "Cloudflare Account ID")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "R2 Access Key ID")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "R2 Secret Access Key")
	cmd.Flags().StringVar(&bucket, "bucket", "", "R2 Bucket Name")
	cmd.Flags().BoolVar(&skipGuide, "skip-guide", false, "Skip the setup guide (for experienced users)")

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
