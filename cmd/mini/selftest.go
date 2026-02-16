package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func selftestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "selftest",
		Short: "Run 5-step smoke test against the Mini",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println()
			fmt.Println(Selected.Render("  mini selftest"))
			fmt.Println(Sep(50))

			passed := 0
			total := 6

			// 1. Config
			fmt.Printf("  %s config\n", check(true))
			fmt.Println(Info.Render(fmt.Sprintf("       host=%s  user=%s", cfg.Host, cfg.SSHUser)))
			passed++

			// 2. SSH
			ssh := sshClient()
			out, err := ssh.Run("echo PONG")
			if err != nil {
				fmt.Printf("  %s ssh: %s\n", check(false), err)
			} else if strings.TrimSpace(out) != "PONG" {
				fmt.Printf("  %s ssh: unexpected response %q\n", check(false), out)
			} else {
				fmt.Printf("  %s ssh\n", check(true))
				passed++
			}

			// 3. Ollama ping
			oc := ollamaClient()
			if err := oc.Ping(); err != nil {
				fmt.Printf("  %s ollama: %s\n", check(false), err)
			} else {
				fmt.Printf("  %s ollama\n", check(true))
				passed++
			}

			// 4. Models
			models, err := oc.Models()
			if err != nil {
				fmt.Printf("  %s models: %s\n", check(false), err)
			} else if len(models) == 0 {
				fmt.Printf("  %s models: no models pulled\n", check(false))
			} else {
				fmt.Printf("  %s models (%d pulled)\n", check(true), len(models))
				passed++
			}

			// 5. Inference — use smallest model
			if len(models) > 0 {
				// Find smallest model
				smallest := models[0]
				for _, m := range models[1:] {
					if m.Size < smallest.Size {
						smallest = m
					}
				}

				fmt.Printf("  %s inference (%s)...", Info.Render("·"), smallest.Name)
				resp, err := oc.Chat(ChatRequest{
					Model: smallest.Name,
					Messages: []ChatMessage{
						{Role: "user", Content: "respond with exactly: PONG"},
					},
					Options: ChatOptions{
						Temperature: 0,
						NumPredict:  10,
					},
				})
				if err != nil {
					fmt.Printf("\r  %s inference: %s\n", check(false), err)
				} else if !strings.Contains(strings.ToUpper(resp.Message.Content), "PONG") {
					fmt.Printf("\r  %s inference: got %q\n", check(false), resp.Message.Content)
				} else {
					toks := FmtTokS(resp.EvalCount, resp.EvalDuration)
					fmt.Printf("\r  %s inference (%s, %s)\n", check(true), smallest.Name, toks)
					passed++
				}
			} else {
				fmt.Printf("  %s inference: skipped (no models)\n", check(false))
			}

			// 6. TTS — generate short audio on Mini, verify mp3 bytes
			fmt.Printf("  %s tts...", Info.Render("·"))
			ttsData, err := ttsOnMini(ssh, "test", "af_heart", 1.0, false)
			if err != nil {
				fmt.Printf("\r  %s tts: %s\n", check(false), err)
			} else if len(ttsData) < 100 {
				fmt.Printf("\r  %s tts: too small (%d bytes)\n", check(false), len(ttsData))
			} else if !(ttsData[0] == 0xFF && ttsData[1]&0xE0 == 0xE0) && string(ttsData[:3]) != "ID3" {
				fmt.Printf("\r  %s tts: not valid mp3\n", check(false))
			} else {
				fmt.Printf("\r  %s tts (%s)\n", check(true), FmtSize(int64(len(ttsData))))
				passed++
			}

			fmt.Println(Sep(50))
			if passed == total {
				fmt.Println(Ready.Render(fmt.Sprintf("  %d/%d passed", passed, total)))
			} else {
				fmt.Println(Blocked.Render(fmt.Sprintf("  %d/%d passed", passed, total)))
			}
			fmt.Println()

			if passed < total {
				os.Exit(1)
			}
			return nil
		},
	}
}

func check(ok bool) string {
	if ok {
		return Ready.Render("✓")
	}
	return Blocked.Render("✗")
}
