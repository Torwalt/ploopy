package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func message(role, outcome string) string {
	switch outcome {
	case "blocked":
		return "BLOCKED the fake scenario asked for it"
	case "nothing":
		return "NOTHING-TO-DO the fake scenario found nothing to do"
	}
	switch role {
	case "collect":
		return "No findings.\n\nDONE"
	case "triage":
		return "Wrote the fix plan.\n\nDONE"
	default:
		return "Worked.\n\nDONE"
	}
}

func sessionID(name string, args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--session-id" {
			return args[i+1]
		}
	}
	return fmt.Sprintf("%s_%d", name, time.Now().UnixNano())
}

func speak(name, role string, sc Scenario, before, after HarnessState, args []string) error {
	text := message(role, sc.Outcome)
	switch name {
	case "claude":
		return speakClaude(sc, before, after, text, sessionID(name, args))
	case "codex":
		return speakCodex(sc, text)
	case "opencode":
		return speakOpencode(sc, text)
	}
	return fmt.Errorf("no format for %s", name)
}

type claudeWindows struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
}

func claudeInfo(status string, h HarnessState, limitReset time.Time, id string) map[string]any {
	sessionReset := h.Session.ResetAt
	if sessionReset.IsZero() {
		sessionReset = limitReset
	}
	weeklyReset := h.Weekly.ResetAt
	if weeklyReset.IsZero() {
		weeklyReset = limitReset
	}
	return map[string]any{
		"type":          "rate_limit_event",
		"session_id":    id,
		"resetsAt":      unix(sessionReset),
		"rateLimitType": "five_hour",
		"rate_limit_info": map[string]any{
			"status":        status,
			"resetsAt":      unix(sessionReset),
			"rateLimitType": "five_hour",
			"unifiedWindows": map[string]any{
				"five_hour": claudeWindows{Utilization: h.Session.Used, ResetsAt: unix(sessionReset)},
				"seven_day": claudeWindows{Utilization: h.Weekly.Used, ResetsAt: unix(weeklyReset)},
			},
		},
	}
}

func claudeUsage() map[string]any {
	return map[string]any{
		"input_tokens":                1200,
		"output_tokens":               40,
		"cache_read_input_tokens":     100,
		"cache_creation_input_tokens": 0,
		"output_tokens_details":       map[string]any{"thinking_tokens": 10},
	}
}

func speakClaude(sc Scenario, before, after HarnessState, text, id string) error {
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	write := func(value any) error { return enc.Encode(value) }

	if err := write(map[string]any{"type": "system", "subtype": "init", "session_id": id, "apiKeySource": "none"}); err != nil {
		return err
	}
	if err := write(claudeInfo("allowed", before, time.Time{}, id)); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "assistant", "session_id": id,
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}},
	}); err != nil {
		return err
	}
	if err := write(claudeInfo("allowed", after, time.Time{}, id)); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "result", "subtype": "success", "is_error": false,
		"result": text, "num_turns": 2, "total_cost_usd": sc.Cost,
		"usage": claudeUsage(), "session_id": id,
	}); err != nil {
		return err
	}
	return out.Flush()
}

func speakClaudeLimit(sc Scenario, state HarnessState, reset time.Time, args []string) error {
	id := sessionID("claude", args)
	text := "You have reached your usage limit. It resets at " +
		reset.In(time.UTC).Format("3:04pm") + " (UTC)."
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	write := func(value any) error { return enc.Encode(value) }

	if err := write(map[string]any{"type": "system", "subtype": "init", "session_id": id, "apiKeySource": "none"}); err != nil {
		return err
	}
	if err := write(claudeInfo("rejected", state, reset, id)); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "assistant", "session_id": id,
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "result", "subtype": "error_rate_limit", "is_error": true,
		"api_error_status": 429, "result": text, "session_id": id,
	}); err != nil {
		return err
	}
	return out.Flush()
}

func speakCodex(_ Scenario, text string) error {
	id := fmt.Sprintf("thread_%d", time.Now().UnixNano())
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	write := func(value any) error { return enc.Encode(value) }

	if err := write(map[string]any{"type": "thread.started", "thread_id": id}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "item.completed",
		"item": map[string]any{"id": "message", "type": "agent_message", "text": text},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "turn.completed",
		"usage": map[string]any{
			"input_tokens": 1000, "cached_input_tokens": 0,
			"output_tokens": 40, "reasoning_output_tokens": 0,
		},
	}); err != nil {
		return err
	}
	return out.Flush()
}

func speakCodexLimit(reset time.Time) error {
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	text := "You've hit your usage limit. It resets at " +
		reset.In(time.UTC).Format("3:04pm") + " (UTC)."
	if err := enc.Encode(map[string]any{"type": "error", "message": text}); err != nil {
		return err
	}
	return out.Flush()
}

func speakOpencode(sc Scenario, text string) error {
	id := fmt.Sprintf("ses_%d", time.Now().UnixNano())
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	write := func(value any) error { return enc.Encode(value) }

	if err := write(map[string]any{
		"type": "step_start", "sessionID": id,
		"part": map[string]any{"type": "step-start"},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "text", "sessionID": id,
		"part": map[string]any{"type": "text", "text": text},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "step_finish", "sessionID": id,
		"part": map[string]any{
			"type": "step-finish", "reason": "stop", "cost": sc.Cost,
			"tokens": map[string]any{
				"input": 100, "output": 10, "reasoning": 0,
				"cache": map[string]any{"read": 0, "write": 0},
			},
		},
	}); err != nil {
		return err
	}
	return out.Flush()
}

func speakOpencodeLimit() error {
	id := fmt.Sprintf("ses_%d", time.Now().UnixNano())
	out := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(out)
	write := func(value any) error { return enc.Encode(value) }

	if err := write(map[string]any{
		"type": "step_start", "sessionID": id,
		"part": map[string]any{"type": "step-start"},
	}); err != nil {
		return err
	}
	if err := write(map[string]any{
		"type": "error", "sessionID": id,
		"error": map[string]any{
			"name": "APIError",
			"data": map[string]any{"message": "you have reached the rate limit for this model"},
		},
	}); err != nil {
		return err
	}
	return out.Flush()
}
