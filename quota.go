package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL  = "https://chatgpt.com/backend-api/wham/usage"
)
const maxUsageBytes = 128 * 1024

type credential struct {
	Type         string `json:"type"`
	AccessToken  string `json:"access_token"`
	AccountUUID  string `json:"account_uuid"`
	AccountID    string `json:"account_id"`
	Organization string `json:"organization_uuid"`
	Disabled     bool   `json:"disabled"`
	ProxyURL     string `json:"proxy_url"`
	BaseURL      string `json:"base_url"`
}

func (c credential) identity() string {
	value := strings.ToLower(strings.TrimSpace(c.Type)) + "\x00" + c.AccountUUID + "\x00" + c.AccountID + "\x00" + c.Organization
	if c.AccountUUID == "" && c.AccountID == "" {
		value += "\x00" + c.AccessToken
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type quotaWindow struct {
	Utilization *float64   `json:"utilization"`
	ResetsAt    *time.Time `json:"resets_at"`
}

type quotaSnapshot struct {
	Provider string       `json:"provider"`
	Weekly   quotaWindow  `json:"seven_day"`
	FiveHour *quotaWindow `json:"five_hour,omitempty"`
	Sonnet   *quotaWindow `json:"seven_day_sonnet,omitempty"`
	Opus     *quotaWindow `json:"seven_day_opus,omitempty"`
	PlanType string       `json:"plan_type,omitempty"`
	ReadAt   time.Time    `json:"read_at"`
	Identity string       `json:"-"`
}

func validateWindow(w *quotaWindow, required bool) bool {
	if w == nil {
		return !required
	}
	if w.Utilization == nil || math.IsNaN(*w.Utilization) || math.IsInf(*w.Utilization, 0) || *w.Utilization < 0 || *w.Utilization > 100 {
		return false
	}
	// A zero-use short window can legitimately have no countdown yet.
	return w.ResetsAt != nil && !w.ResetsAt.IsZero() || !required && *w.Utilization == 0
}

func parseQuota(raw []byte) (quotaSnapshot, error) {
	var snap quotaSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, errors.New("invalid_quota_json")
	}
	if !validateWindow(&snap.Weekly, true) || !validateWindow(snap.FiveHour, false) || !validateWindow(snap.Sonnet, false) || !validateWindow(snap.Opus, false) {
		return quotaSnapshot{}, errors.New("invalid_quota_windows")
	}
	return snap, nil
}

func parseCodexQuota(raw []byte, now time.Time) (quotaSnapshot, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return quotaSnapshot{}, errors.New("invalid_quota_json")
	}
	rateLimit, ok := mapValue(document, "rate_limit", "rateLimit")
	if !ok {
		return quotaSnapshot{}, errors.New("invalid_codex_rate_limit")
	}
	snapshot := quotaSnapshot{Provider: "codex", PlanType: stringValue(document, "plan_type", "planType")}
	for index, names := range [][2]string{{"primary_window", "primaryWindow"}, {"secondary_window", "secondaryWindow"}} {
		windowRaw, ok := mapValue(rateLimit, names[0], names[1])
		if !ok {
			continue
		}
		window, kind, valid := parseCodexWindow(windowRaw, now, index)
		if !valid {
			return quotaSnapshot{}, errors.New("invalid_codex_window")
		}
		switch kind {
		case "five_hour":
			snapshot.FiveHour = &window
		case "long":
			if snapshot.Weekly.ResetsAt == nil || window.ResetsAt.Before(*snapshot.Weekly.ResetsAt) {
				snapshot.Weekly = window
			}
		}
	}
	if !validateWindow(&snapshot.Weekly, true) {
		return quotaSnapshot{}, errors.New("invalid_codex_weekly_window")
	}
	return snapshot, nil
}

func parseCodexWindow(raw map[string]any, now time.Time, position int) (quotaWindow, string, bool) {
	used, ok := numberValue(raw, "used_percent", "usedPercent")
	if !ok || math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 {
		return quotaWindow{}, "", false
	}
	seconds, hasSeconds := numberValue(raw, "limit_window_seconds", "limitWindowSeconds")
	kind := "long"
	if hasSeconds && seconds == 18000 {
		kind = "five_hour"
	} else if hasSeconds && seconds != 604800 && (seconds < 2419200 || seconds > 2678400) {
		return quotaWindow{}, "", false
	} else if !hasSeconds && position == 0 {
		kind = "five_hour"
	}
	reset, hasReset := codexResetAt(raw, now)
	allowed, hasAllowed := boolValue(raw, "allowed")
	limitReached, hasLimitReached := boolValue(raw, "limit_reached", "limitReached")
	if hasAllowed && !allowed {
		used = 100
	}
	if hasLimitReached && limitReached {
		used = 100
	}
	window := quotaWindow{Utilization: &used}
	if hasReset {
		window.ResetsAt = &reset
	}
	if window.ResetsAt == nil && used != 0 {
		return quotaWindow{}, "", false
	}
	return window, kind, true
}

func codexResetAt(raw map[string]any, now time.Time) (time.Time, bool) {
	if seconds, ok := numberValue(raw, "reset_at", "resetAt"); ok && seconds > 0 {
		return time.Unix(int64(seconds), 0).UTC(), true
	}
	if seconds, ok := numberValue(raw, "reset_after_seconds", "resetAfterSeconds"); ok && seconds >= 0 {
		return now.Add(time.Duration(seconds * float64(time.Second))), true
	}
	return time.Time{}, false
}

func mapValue(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, ok := value.(map[string]any)
				return result, ok
			}
		}
	}
	return nil, false
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, _ := value.(string)
				return result
			}
		}
	}
	return ""
}

func numberValue(m map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if !strings.EqualFold(actual, key) {
				continue
			}
			switch result := value.(type) {
			case float64:
				return result, true
			case json.Number:
				parsed, err := result.Float64()
				return parsed, err == nil
			}
		}
	}
	return 0, false
}

func boolValue(m map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		for actual, value := range m {
			if strings.EqualFold(actual, key) {
				result, ok := value.(bool)
				return result, ok
			}
		}
	}
	return false, false
}

func (s quotaSnapshot) availability(model string, now time.Time, maxAge time.Duration) string {
	if s.ReadAt.IsZero() || now.Before(s.ReadAt) || now.Sub(s.ReadAt) >= maxAge {
		return "stale"
	}
	windows := []*quotaWindow{&s.Weekly, s.FiveHour}
	model = strings.ToLower(model)
	if strings.Contains(model, "sonnet") {
		windows = append(windows, s.Sonnet)
	}
	if strings.Contains(model, "opus") {
		windows = append(windows, s.Opus)
	}
	for _, w := range windows {
		if w == nil {
			continue
		}
		if w.Utilization == nil {
			return "unknown"
		}
		if w.ResetsAt != nil && !w.ResetsAt.After(now) {
			return "reset_pending_refresh"
		}
		if *w.Utilization >= 100 {
			return "exhausted"
		}
	}
	return "ready"
}

type quotaFetcher interface {
	Fetch(context.Context, credential) (quotaSnapshot, error)
}

type httpQuotaFetcher struct {
	client *http.Client
}

func newHTTPFetcher() *httpQuotaFetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 2
	transport.MaxIdleConnsPerHost = 1
	return &httpQuotaFetcher{client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type quotaError struct {
	code       string
	retryAfter time.Duration
}

func (e *quotaError) Error() string { return e.code }

func (f *httpQuotaFetcher) Fetch(ctx context.Context, cred credential) (quotaSnapshot, error) {
	provider := strings.ToLower(strings.TrimSpace(cred.Type))
	if (provider != "claude" && provider != "codex") || cred.Disabled || strings.TrimSpace(cred.AccessToken) == "" {
		return quotaSnapshot{}, errors.New("ineligible_credential")
	}
	// Quota polling connects directly; refuse rather than bypass a per-auth proxy or custom endpoint.
	if cred.ProxyURL != "" || cred.BaseURL != "" {
		return quotaSnapshot{}, errors.New("custom_transport_unsupported")
	}
	usageURL := claudeUsageURL
	if provider == "codex" {
		usageURL = codexUsageURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return quotaSnapshot{}, errors.New("quota_request_failed")
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("Accept", "application/json")
	if provider == "claude" {
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("User-Agent", "codex_cli_rs/0.76.0")
		if strings.TrimSpace(cred.AccountID) != "" {
			req.Header.Set("Chatgpt-Account-Id", cred.AccountID)
		}
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return quotaSnapshot{}, errors.New("quota_transport_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		retry := time.Duration(0)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			retry = retryAfter(resp.Header.Get("Retry-After"), time.Now())
		}
		return quotaSnapshot{}, &quotaError{code: fmt.Sprintf("quota_http_%d", resp.StatusCode), retryAfter: retry}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUsageBytes+1))
	if err != nil || len(body) > maxUsageBytes {
		return quotaSnapshot{}, errors.New("invalid_quota_body")
	}
	var snap quotaSnapshot
	if provider == "claude" {
		snap, err = parseQuota(body)
	} else {
		snap, err = parseCodexQuota(body, time.Now().UTC())
	}
	if err != nil {
		return quotaSnapshot{}, err
	}
	snap.ReadAt = time.Now().UTC()
	snap.Provider = provider
	snap.Identity = cred.identity()
	return snap, nil
}

func retryAfter(raw string, now time.Time) time.Duration {
	delay := time.Duration(0)
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		if seconds > 3600 {
			return time.Hour
		}
		if seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	} else if deadline, err := http.ParseTime(raw); err == nil {
		delay = deadline.Sub(now)
	}
	return min(time.Hour, max(0, delay))
}
