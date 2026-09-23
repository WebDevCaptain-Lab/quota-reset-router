package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const validQuota = `{"seven_day":{"utilization":20,"resets_at":"2026-09-25T00:00:00Z"},"five_hour":{"utilization":0,"resets_at":null},"seven_day_sonnet":null,"unrecognized_field":{"value":1}}`

func TestParseQuota(t *testing.T) {
	cases := []struct {
		name, raw string
		valid     bool
	}{
		{"normal", validQuota, true},
		{"minimal", `{"seven_day":{"utilization":0,"resets_at":"2026-09-25T00:00:00+02:00"}}`, true},
		{"exhausted", `{"seven_day":{"utilization":100,"resets_at":"2026-09-25T00:00:00Z"}}`, true},
		{"unknown weekly", `{"seven_day":null}`, false},
		{"unstarted weekly", `{"seven_day":{"utilization":0,"resets_at":null}}`, false},
		{"missing utilization", `{"seven_day":{"resets_at":"2026-09-25T00:00:00Z"}}`, false},
		{"null utilization", `{"seven_day":{"utilization":null,"resets_at":"2026-09-25T00:00:00Z"}}`, false},
		{"negative", `{"seven_day":{"utilization":-1,"resets_at":"2026-09-25T00:00:00Z"}}`, false},
		{"above 100", `{"seven_day":{"utilization":101,"resets_at":"2026-09-25T00:00:00Z"}}`, false},
		{"wrong units type", `{"seven_day":{"utilization":"20","resets_at":"2026-09-25T00:00:00Z"}}`, false},
		{"bad timestamp", `{"seven_day":{"utilization":20,"resets_at":"tomorrow"}}`, false},
		{"zero timestamp", `{"seven_day":{"utilization":20,"resets_at":"0001-01-01T00:00:00Z"}}`, false},
		{"short malformed", `{"seven_day":{"utilization":20,"resets_at":"2026-09-25T00:00:00Z"},"five_hour":{"utilization":100,"resets_at":null}}`, false},
		{"invalid JSON", `{"seven_day":`, false},
		{"null", `null`, false},
		{"empty", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseQuota([]byte(tc.raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
	snap, err := parseQuota([]byte(validQuota))
	if err != nil || *snap.Weekly.Utilization != 20 {
		t.Fatal("utilization must remain percentage points")
	}
}

func TestParseCodexQuotaWindows(t *testing.T) {
	raw := []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":60,"limit_window_seconds":18000,"reset_at":1790172000,"reset_after_seconds":7200},"secondary_window":{"used_percent":30,"limit_window_seconds":604800,"reset_at":1790683200,"reset_after_seconds":518400}}}`)
	snapshot, err := parseCodexQuota(raw, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PlanType != "plus" || snapshot.FiveHour == nil || snapshot.Weekly.ResetsAt == nil {
		t.Fatalf("missing codex windows: %+v", snapshot)
	}
	if *snapshot.FiveHour.Utilization != 60 || *snapshot.Weekly.Utilization != 30 {
		t.Fatalf("wrong codex utilization: %+v", snapshot)
	}
	if snapshot.FiveHour.ResetsAt.Before(testNow) || snapshot.Weekly.ResetsAt.Before(testNow) {
		t.Fatalf("wrong codex reset timestamps: %+v", snapshot)
	}
	weeklyOnly, err := parseCodexQuota([]byte(`{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_after_seconds":360000}}}`), testNow)
	if err != nil || weeklyOnly.FiveHour != nil || weeklyOnly.Weekly.ResetsAt == nil {
		t.Fatalf("weekly-only Codex plan was not parsed: %+v, %v", weeklyOnly, err)
	}
}

func TestParseCodexRejectsAmbiguousOrInvalidWindows(t *testing.T) {
	for _, raw := range []string{
		`{"rate_limit":{}}`,
		`{"rate_limit":{"primary_window":{"used_percent":101,"limit_window_seconds":18000,"reset_at":1}}}`,
		`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":123,"reset_at":1}}}`,
		`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000}}}`,
		`{"rate_limit":{"secondary_window":{"used_percent":10,"limit_window_seconds":604800}}}`,
		`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":"bad"},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_at":1}}}`,
	} {
		if _, err := parseCodexQuota([]byte(raw), testNow); err == nil {
			t.Fatalf("accepted invalid Codex payload: %s", raw)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testCredential() credential {
	return credential{Type: "claude", AccessToken: "SECRET_TOKEN", AccountUUID: "a"}
}

func TestHTTPQuotaRequestAndIdentity(t *testing.T) {
	f := newHTTPFetcher()
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != claudeUsageURL || r.Method != http.MethodGet || r.Body != nil {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer SECRET_TOKEN" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Fatal("missing OAuth headers")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(validQuota))}, nil
	})
	snap, err := f.Fetch(context.Background(), testCredential())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Identity != testCredential().identity() || snap.ReadAt.IsZero() {
		t.Fatal("missing identity or freshness")
	}
	encoded, _ := json.Marshal(snap)
	if strings.Contains(string(encoded), "SECRET_TOKEN") || strings.Contains(string(encoded), snap.Identity) {
		t.Fatal("identity leaked")
	}
}

func TestHTTPCodeQuotaRequestAndIdentity(t *testing.T) {
	f := newHTTPFetcher()
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != codexUsageURL || r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer CODEX_TOKEN" || r.Header.Get("Chatgpt-Account-Id") != "account-id" || r.Header.Get("User-Agent") == "" {
			t.Fatalf("unexpected Codex request: %s %+v", r.URL, r.Header)
		}
		body := `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":45,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":3600}}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	cred := credential{Type: "codex", AccessToken: "CODEX_TOKEN", AccountID: "account-id"}
	snapshot, err := f.Fetch(context.Background(), cred)
	if err != nil || snapshot.Provider != "codex" || snapshot.Identity != cred.identity() {
		t.Fatalf("bad Codex fetch: %+v, %v", snapshot, err)
	}
}

func TestHTTPFailureHandling(t *testing.T) {
	for _, status := range []int{301, 401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newHTTPFetcher()
			f.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"720"}}, Body: io.NopCloser(strings.NewReader("SECRET_TOKEN"))}, nil
			})
			_, err := f.Fetch(context.Background(), testCredential())
			var qerr *quotaError
			if !errors.As(err, &qerr) || strings.Contains(err.Error(), "SECRET_TOKEN") {
				t.Fatalf("bad error: %v", err)
			}
			if status == 429 && qerr.retryAfter != 12*time.Minute {
				t.Fatalf("lost Retry-After: %+v", qerr)
			}
		})
	}
	for _, body := range []string{"SECRET_TOKEN", strings.Repeat("a", maxUsageBytes+1)} {
		f := newHTTPFetcher()
		f.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := f.Fetch(context.Background(), testCredential()); err == nil || strings.Contains(err.Error(), "SECRET_TOKEN") {
			t.Fatalf("unsanitized body failure: %v", err)
		}
	}
}

func TestCredentialsDoNotEscapeThroughRedirects(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled = true }))
	defer target.Close()
	f := newHTTPFetcher()
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != claudeUsageURL {
			return http.DefaultTransport.RoundTrip(r)
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if _, err := f.Fetch(context.Background(), testCredential()); err == nil {
		t.Fatal("redirect accepted")
	}
	if targetCalled {
		t.Fatal("redirect destination was contacted")
	}
}

func TestCredentialEligibilityAndCancellation(t *testing.T) {
	for _, change := range []func(*credential){
		func(c *credential) { c.Disabled = true },
		func(c *credential) { c.Type = "unsupported" },
		func(c *credential) { c.AccessToken = "" },
		func(c *credential) { c.ProxyURL = "http://proxy.test" },
		func(c *credential) { c.BaseURL = "https://custom.test" },
	} {
		c := testCredential()
		change(&c)
		f := newHTTPFetcher()
		f.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("ineligible credential reached network")
			return nil, nil
		})
		if _, err := f.Fetch(context.Background(), c); err == nil {
			t.Fatal("ineligible credential accepted")
		}
	}
	f := newHTTPFetcher()
	f.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, errors.New("SECRET_TOKEN")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Fetch(ctx, testCredential()); err == nil || strings.Contains(err.Error(), "SECRET_TOKEN") {
		t.Fatalf("bad cancellation error: %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"120", 2 * time.Minute}, {"-5", 0}, {"9999999999", time.Hour}, {"not a date", 0},
		{testNow.Add(3 * time.Minute).Format(http.TimeFormat), 3 * time.Minute},
		{testNow.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		if got := retryAfter(tc.raw, testNow); got != tc.want {
			t.Fatalf("%s: got %s, want %s", tc.raw, got, tc.want)
		}
	}
}

func FuzzParseQuota(f *testing.F) {
	f.Add([]byte(validQuota))
	f.Add([]byte(`{"seven_day":null}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := parseQuota(raw)
		if err == nil && !validateWindow(&s.Weekly, true) {
			t.Fatal("accepted invalid weekly quota")
		}
	})
}
