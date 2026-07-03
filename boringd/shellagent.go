package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// The terminal agent drives a shell to accomplish a natural-language goal. It
// types real commands into the guest's serial console (so a user watching the
// terminal sees the AI work) and reads their output back by watching for the
// shell prompt. Narration streams to the browser over the same JSON protocol as
// the computer-use agent (say / action / done / error).

const shellAgentSystem = `You drive a Linux shell to accomplish the user's goal. This is a LIVE demo on a public website — a real person is watching the terminal as you type.

The shell is Alpine Linux with python3, pip, node, npm, git, curl and full internet access. You run as root. Use the run_command tool to run ONE command at a time; you get its combined output back.

Before each command, write ONE short, friendly, first-person sentence about what you're about to do (e.g. "Let me check what Python version is here." or "Installing the requests library now."). One sentence — don't over-explain.

Keep commands non-interactive (use flags like -y, --quiet). Don't run commands that block forever (no top, no servers in the foreground — background them with & if needed). You have a limited number of steps, so be efficient. When the goal is achieved, reply with one sentence starting with "Done:" and stop calling tools.`

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\r`)
var promptRe = regexp.MustCompile(`(?m)^boring:[^\n]*[#$]\s*$`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func (s *Server) runShellAgent(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	id := r.PathValue("id")
	goal := strings.TrimSpace(r.URL.Query().Get("goal"))
	if goal == "" {
		goal = "Print a friendly greeting and today's date."
	}
	if len(goal) > 400 {
		goal = goal[:400]
	}

	console, ok := s.mgr.Console(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	send := func(typ, text string) { _ = conn.WriteJSON(map[string]string{"type": typ, "text": text}) }

	if s.cfg.AnthropicKey == "" {
		send("error", "the agent isn't configured on this server")
		return
	}
	if n := atomic.AddInt32(&agentRuns, 1); int(n) > s.cfg.AgentMaxConcurrent {
		atomic.AddInt32(&agentRuns, -1)
		send("error", "too many agents are running right now — try again in a moment")
		return
	}
	defer atomic.AddInt32(&agentRuns, -1)

	stop := make(chan struct{})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(stop)
				return
			}
		}
	}()
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}

	_, sub := console.Subscribe()
	defer console.Unsubscribe(sub)

	tool := map[string]any{
		"name":        "run_command",
		"description": "Run one shell command in the Linux terminal and get its combined stdout/stderr back.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string", "description": "the shell command to run"}},
			"required":   []string{"command"},
		},
	}
	messages := []json.RawMessage{userTextMessage("Your task: " + goal)}

	send("say", "On it — let me get to work in the terminal.")
	for step := 0; step < s.cfg.AgentMaxSteps; step++ {
		if stopped() {
			return
		}
		resp, err := callAnthropicShell(s.cfg, tool, messages)
		if err != nil {
			send("error", err.Error())
			return
		}
		messages = append(messages, assistantMessage(resp.Content))

		var results []json.RawMessage
		for _, raw := range resp.Content {
			var b blockHead
			if json.Unmarshal(raw, &b) != nil {
				continue
			}
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					send("say", t)
				}
			case "tool_use":
				if stopped() {
					return
				}
				cmd, _ := b.Input["command"].(string)
				cmd = strings.TrimSpace(cmd)
				if cmd == "" {
					results = append(results, textToolResult(b.ID, "(empty command)", true))
					continue
				}
				send("action", "$ "+cmd)
				out := runGuestCommand(console, sub, cmd, 30*time.Second)
				results = append(results, textToolResult(b.ID, out, false))
			}
		}
		if len(results) == 0 {
			send("done", "")
			return
		}
		messages = append(messages, userToolResults(results))
	}
	send("done", "reached the step limit")
}

// runGuestCommand types a command into the guest console and returns its output,
// captured by watching for the shell prompt to reappear.
func runGuestCommand(console *Console, sub *consoleSub, cmd string, timeout time.Duration) string {
	// Drain anything buffered so we only read this command's output.
	for {
		select {
		case <-sub.ch:
			continue
		default:
		}
		break
	}
	if _, err := console.Write([]byte(cmd + "\n")); err != nil {
		return "[the terminal is gone]"
	}
	var buf bytes.Buffer
	deadline := time.After(timeout)
	for {
		select {
		case chunk, ok := <-sub.ch:
			if !ok {
				return finalizeOutput(buf.String(), cmd)
			}
			buf.Write(chunk)
			// The prompt reappears once the command finishes.
			if promptRe.MatchString(stripANSI(buf.String())) {
				// Small grace period for any trailing bytes.
				time.Sleep(60 * time.Millisecond)
				for {
					select {
					case c2, ok2 := <-sub.ch:
						if ok2 {
							buf.Write(c2)
							continue
						}
					default:
					}
					break
				}
				return finalizeOutput(buf.String(), cmd)
			}
		case <-deadline:
			return finalizeOutput(buf.String(), cmd) + "\n[still running — moved on]"
		}
	}
}

// finalizeOutput strips the echoed command line and the trailing prompt, leaving
// just the command's output (capped so a huge dump doesn't blow the context).
func finalizeOutput(raw, cmd string) string {
	s := stripANSI(raw)
	// Drop the trailing prompt line.
	if loc := promptRe.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	// Drop the first line if it's the echoed command.
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		first := strings.TrimSpace(s[:i])
		if first == strings.TrimSpace(cmd) || strings.HasSuffix(first, cmd) {
			s = s[i+1:]
		}
	}
	s = strings.Trim(s, "\r\n")
	if len(s) > 6000 {
		s = s[:6000] + "\n…(truncated)"
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

func callAnthropicShell(cfg Config, tool map[string]any, messages []json.RawMessage) (*apiResp, error) {
	body := map[string]any{
		"model":         cfg.AgentModel,
		"max_tokens":    1024,
		"system":        shellAgentSystem,
		"tools":         []any{tool},
		"messages":      messages,
		"output_config": map[string]any{"effort": "low"},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(buf))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", cfg.AnthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	res, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("the AI is unreachable right now")
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error.Message != "" {
			return nil, fmt.Errorf("model error: %s", e.Error.Message)
		}
		return nil, fmt.Errorf("model http %d", res.StatusCode)
	}
	var out apiResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("bad model response")
	}
	return &out, nil
}

func userTextMessage(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return b
}

func textToolResult(id, content string, isErr bool) json.RawMessage {
	m := map[string]any{"type": "tool_result", "tool_use_id": id, "content": content}
	if isErr {
		m["is_error"] = true
	}
	b, _ := json.Marshal(m)
	return b
}
