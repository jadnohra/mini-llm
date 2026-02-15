package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func chatCmd() *cobra.Command {
	var (
		model   string
		temp    float64
		history bool
		tokens  bool
	)

	cmd := &cobra.Command{
		Use:   "chat [SESSION] PROMPT",
		Short: "Multi-turn chat with session memory",
		Long: `Send a prompt within a named session. The full conversation history is replayed each turn.

If SESSION matches an existing session, continue it. Otherwise a new session
is created with an auto-generated name (e.g. capybara-314).

  mini chat "write fibonacci in Go"              # new session, auto-named
  mini chat capybara-314 "add memoization"        # continue existing
  mini chat my-project "hello"                    # new session, user-named
  mini chat capybara-314 --history                # view history`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var sessionName, prompt string

			// If first arg is an existing session, use it
			if SessionExists(args[0]) {
				sessionName = args[0]
				if len(args) > 1 {
					prompt = strings.Join(args[1:], " ")
				}
			} else if history || tokens {
				// Flags that need a session name — treat first arg as name
				sessionName = args[0]
			} else {
				// No existing session — all args are the prompt, auto-generate name
				prompt = strings.Join(args, " ")
				sessionName = GenerateSlug()
			}

			sess, err := OpenSession(sessionName)
			if err != nil {
				return err
			}

			if history {
				return printHistory(sess)
			}
			if tokens {
				return printTokens(sess)
			}

			if prompt == "" {
				return fmt.Errorf("provide a prompt: mini chat %s \"your prompt\"", sessionName)
			}

			// Model override
			if model != "" && model != sess.Config.Model {
				sess.Config.Model = model
				if err := sess.saveConfig(); err != nil {
					return fmt.Errorf("save model config: %w", err)
				}
			}

			// Temperature override
			effectiveTemp := sess.Config.Temperature
			if temp >= 0 {
				effectiveTemp = temp
			}

			turn := sess.Turn() + 1

			// Add user message to in-memory history for the request,
			// but don't write to disk yet — if Ctrl+C interrupts streaming,
			// the turn leaves no trace.
			userEntry := HistoryEntry{
				Role:    "user",
				Content: prompt,
				Turn:    turn,
				Time:    time.Now().UTC().Format(time.RFC3339),
			}
			sess.History = append(sess.History, userEntry)

			// Build request
			req := ChatRequest{
				Model:    sess.Config.Model,
				Messages: sess.Messages(),
				Options: ChatOptions{
					Temperature: effectiveTemp,
					NumPredict:  sess.Config.MaxTokens,
				},
			}

			// Stream response, capturing full text
			fmt.Println()
			var buf bytes.Buffer
			w := io.MultiWriter(os.Stdout, &buf)
			oc := ollamaClient()
			final, err := oc.ChatStream(req, w)
			if err != nil {
				return err
			}
			fmt.Println()

			// Compute tok/s
			var tokS float64
			if final.EvalDuration > 0 {
				tokS = float64(final.EvalCount) / (float64(final.EvalDuration) / 1e9)
			}

			// Save both user + assistant to disk atomically (after successful stream)
			if err := sess.AppendHistory(userEntry); err != nil {
				return fmt.Errorf("save user message: %w", err)
			}
			asstEntry := HistoryEntry{
				Role:    "assistant",
				Content: buf.String(),
				Tokens:  final.EvalCount,
				Turn:    turn,
				TokS:    math.Round(tokS*10) / 10,
				Time:    time.Now().UTC().Format(time.RFC3339),
			}
			if err := sess.AppendHistory(asstEntry); err != nil {
				return fmt.Errorf("save assistant message: %w", err)
			}

			// Summary
			contextUsed := final.PromptEvalCount + final.EvalCount
			pct := float64(contextUsed) / float64(sess.Config.ContextLimit) * 100

			fmt.Println(Sep(50))
			fmt.Println(Info.Render(fmt.Sprintf("  %s · turn %d · %s · %d tok · %s",
				sess.Name, turn, sess.Config.Model,
				final.EvalCount, FmtTokS(final.EvalCount, final.EvalDuration))))
			fmt.Println(Info.Render(fmt.Sprintf("  %s / %s tokens · %.0f%% used",
				fmtK(contextUsed), fmtK(sess.Config.ContextLimit), pct)))

			return nil
		},
	}

	cmd.Flags().StringVarP(&model, "model", "m", "", "model for this turn (updates session)")
	cmd.Flags().Float64VarP(&temp, "temp", "t", -1, "temperature override")
	cmd.Flags().BoolVar(&history, "history", false, "print conversation history")
	cmd.Flags().BoolVar(&tokens, "tokens", false, "show token usage summary")

	return cmd
}

func printHistory(sess *Session) error {
	if len(sess.History) == 0 {
		fmt.Println(Info.Render("  (empty session)"))
		return nil
	}
	fmt.Println()
	fmt.Println(Selected.Render(fmt.Sprintf("  %s · %d turns", sess.Name, sess.Turn())))
	fmt.Println(Sep(50))
	for _, e := range sess.History {
		switch e.Role {
		case "user":
			fmt.Printf("\n  %s  %s\n", Selected.Render("you:"), e.Content)
		case "assistant":
			content := e.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			fmt.Printf("  %s  %s\n", Info.Render("llm:"), content)
			if e.Tokens > 0 {
				fmt.Printf("       %s\n", Dim.Render(fmt.Sprintf("%d tok · %.1f tok/s", e.Tokens, e.TokS)))
			}
		}
	}
	fmt.Println()
	return nil
}

func printTokens(sess *Session) error {
	total := 0
	for _, e := range sess.History {
		total += e.Tokens
	}
	pct := float64(total) / float64(sess.Config.ContextLimit) * 100

	fmt.Println()
	fmt.Println(Selected.Render(fmt.Sprintf("  %s · tokens", sess.Name)))
	fmt.Println(Sep(50))
	fmt.Println(Row("model", sess.Config.Model))
	fmt.Println(Row("turns", fmt.Sprintf("%d", sess.Turn())))
	fmt.Println(Row("output tokens", fmt.Sprintf("%s (%.0f%% of %s context)",
		fmtK(total), pct, fmtK(sess.Config.ContextLimit))))
	fmt.Println()
	return nil
}

func fmtK(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
