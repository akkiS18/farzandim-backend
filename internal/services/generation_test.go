package services

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReportGenerationProvenance(t *testing.T) {
	old := geminiHTTPClient
	defer func() { geminiHTTPClient = old }()
	cases := []struct {
		name, key, body, source, reason string
		status                          int
		network                         bool
	}{
		{name: "missing key", source: "template", reason: "api_key_missing"},
		{name: "success", key: "test", status: 200, body: `{"candidates":[{"content":{"parts":[{"text":"Real AI report"}]}}]}`, source: "ai"},
		{name: "empty response", key: "test", status: 200, body: `{"candidates":[{"content":{"parts":[{"text":"  "}]}}]}`, source: "template", reason: "empty_response"},
		{name: "quota", key: "test", status: 429, body: `{}`, source: "template", reason: "http_429"},
		{name: "network", key: "test", network: true, source: "template", reason: "network_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GEMINI_API_KEY", tc.key)
			geminiHTTPClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				if tc.network {
					return nil, errors.New("private URL with api key must not be persisted")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			result, err := GenerateAIWeeklyReportResult(nil, StudentWeeklyDataContext{StudentName: "Test Student"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Source != tc.source || result.Reason != tc.reason || result.Text == "" {
				t.Fatalf("unexpected result: %+v", result)
			}
			if result.Source == "ai" && result.Model == "" {
				t.Fatal("AI model missing")
			}
		})
	}
}

func TestAnnouncementAlbumPayload(t *testing.T) {
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	for _, count := range []int{1, 2, 10} {
		t.Run(string(rune('A'+count)), func(t *testing.T) {
			urls := make([]string, count)
			for i := range urls {
				urls[i] = "https://images.example/test.png"
			}
			http.DefaultTransport = testTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				defer r.MultipartForm.RemoveAll()
				if count == 1 {
					if !strings.HasSuffix(r.URL.Path, "/sendPhoto") || r.FormValue("photo") != urls[0] || r.FormValue("caption") != "Hello" {
						t.Fatal("invalid single photo request")
					}
				} else {
					if !strings.HasSuffix(r.URL.Path, "/sendMediaGroup") || !strings.Contains(r.FormValue("media"), `"caption":"Hello"`) {
						t.Fatal("invalid album request")
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
			})
			if err := sendAnnouncementMedia("test", 123, urls, "Hello"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
