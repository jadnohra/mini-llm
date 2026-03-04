package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func lipsyncCmd() *cobra.Command {
	var (
		video    string
		audio    string
		output   string
		steps    int
		guidance float64
		setup    bool
	)

	cmd := &cobra.Command{
		Use:   "lipsync",
		Short: "Lip-sync a face video to audio (LatentSync on Mini)",
		Long: `Generate a lip-synced video using LatentSync on the Mac Mini (MPS).

  mini lipsync --video face.mp4 --audio speech.wav -o output.mp4
  mini lipsync --setup                    # first-time setup only
  mini lipsync --video face.mp4 --audio speech.wav -o output.mp4 --steps 30`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ssh := sshClient()
			repoDir := "~/mini-llm"
			pathPrefix := "export PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH"
			lipsyncDir := repoDir + "/mini-tools/lipsync"
			lipsyncPy := lipsyncDir + "/lipsync.py"

			// Setup-only mode
			if setup {
				fmt.Println()
				fmt.Println(Selected.Render("  mini lipsync --setup"))
				fmt.Println(Sep(50))

				fmt.Println(Info.Render("  running setup on Mini (clone + deps + checkpoints)..."))
				fmt.Println()
				err := ssh.RunInteractive(fmt.Sprintf("%s && cd %s && python3 %s --setup",
					pathPrefix, lipsyncDir, lipsyncPy))
				if err != nil {
					return fmt.Errorf("setup failed: %w", err)
				}
				fmt.Println(Row("status", StatusOK("setup complete")))
				fmt.Println()
				return nil
			}

			// Validate inputs
			if video == "" || audio == "" || output == "" {
				return fmt.Errorf("--video, --audio, and -o/--output are required")
			}
			if _, err := os.Stat(video); err != nil {
				return fmt.Errorf("video not found: %s", video)
			}
			if _, err := os.Stat(audio); err != nil {
				return fmt.Errorf("audio not found: %s", audio)
			}

			t0 := time.Now()

			fmt.Println()
			fmt.Println(Selected.Render("  mini lipsync"))
			fmt.Println(Sep(50))
			fmt.Println(Row("video", video))
			fmt.Println(Row("audio", audio))
			fmt.Println(Row("output", output))
			fmt.Println(Row("steps", fmt.Sprintf("%d", steps)))
			fmt.Println(Row("guidance", fmt.Sprintf("%.1f", guidance)))
			fmt.Println()

			// Create remote tmp dir
			ts := time.Now().UnixNano()
			remoteDir := fmt.Sprintf("/tmp/mini-lipsync-%d", ts)
			if _, err := ssh.Run(fmt.Sprintf("mkdir -p %s", remoteDir)); err != nil {
				return fmt.Errorf("cannot create remote dir: %w", err)
			}

			remoteVideo := fmt.Sprintf("%s/%s", remoteDir, filepath.Base(video))
			remoteAudio := fmt.Sprintf("%s/%s", remoteDir, filepath.Base(audio))
			remoteOutput := fmt.Sprintf("%s/output.mp4", remoteDir)

			// Upload video + audio
			fmt.Fprintln(os.Stderr, Info.Render("  uploading files..."))
			if err := ssh.Push(video, remoteVideo); err != nil {
				return fmt.Errorf("upload video failed: %w", err)
			}
			if err := ssh.Push(audio, remoteAudio); err != nil {
				return fmt.Errorf("upload audio failed: %w", err)
			}

			// Run inference
			inferCmd := fmt.Sprintf(
				"%s && cd %s && python3 %s --video %s --audio %s --output %s --steps %d --guidance %.1f",
				pathPrefix, lipsyncDir, lipsyncPy,
				remoteVideo, remoteAudio, remoteOutput,
				steps, guidance,
			)
			fmt.Fprintln(os.Stderr, Info.Render("  running inference on Mini..."))
			if err := ssh.RunInteractive(inferCmd); err != nil {
				// Cleanup remote
				ssh.Run(fmt.Sprintf("rm -rf %s", remoteDir))
				return fmt.Errorf("inference failed: %w\n  try: mini lipsync --setup", err)
			}

			// Download output
			fmt.Fprintln(os.Stderr, Info.Render("  downloading output..."))
			if err := ssh.Pull(remoteOutput, output); err != nil {
				ssh.Run(fmt.Sprintf("rm -rf %s", remoteDir))
				return fmt.Errorf("download output failed: %w", err)
			}

			// Cleanup remote
			ssh.Run(fmt.Sprintf("rm -rf %s", remoteDir))

			// Summary
			elapsed := time.Since(t0)
			info, _ := os.Stat(output)
			fmt.Println()
			fmt.Println(Sep(50))
			fmt.Println(Row("output", output))
			if info != nil {
				fmt.Println(Row("size", FmtSize(info.Size())))
			}
			fmt.Println(Row("elapsed", elapsed.Truncate(time.Second).String()))
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&video, "video", "", "input face video (required)")
	cmd.Flags().StringVar(&audio, "audio", "", "input audio (required)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output video path (required)")
	cmd.Flags().IntVar(&steps, "steps", 20, "inference steps")
	cmd.Flags().Float64Var(&guidance, "guidance", 1.5, "guidance scale")
	cmd.Flags().BoolVar(&setup, "setup", false, "run setup only (clone + patch + checkpoints)")

	return cmd
}
