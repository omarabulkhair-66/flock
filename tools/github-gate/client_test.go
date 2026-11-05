package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientReadsAndWritesGitHubResources(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/pulls/87":
			_, _ = io.WriteString(w, `{"number":87,"state":"open","body":"Fixes #73","user":{"login":"contributor","type":"User"},"base":{"ref":"main"}}`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"read"}`)
		case "GET /repos/flock-php/flock/pulls/87/files":
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<`+serverURL(r)+`/repos/flock-php/flock/pulls/87/files?page=2>; rel="next"`)
				_, _ = io.WriteString(w, `[{"filename":"docs/one.md"}]`)
				return
			}
			_, _ = io.WriteString(w, `[{"filename":"docs/two.md"}]`)
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"state":"open","labels":[{"name":"help wanted"}]}`)
		case "GET /repos/flock-php/flock/issues/87/comments":
			_, _ = io.WriteString(w, `[{"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:closed -->","created_at":"2026-01-01T00:00:00Z"}]`)
		case "GET /repos/flock-php/flock/issues/87/events":
			_, _ = io.WriteString(w, `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"}]`)
		case "POST /repos/flock-php/flock/issues/87/comments", "PATCH /repos/flock-php/flock/pulls/87", "POST /repos/flock-php/flock/issues/73/labels":
			body, _ := io.ReadAll(r.Body)
			writes = append(writes, r.Method+" "+r.URL.Path+" "+string(body))
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	pr, err := client.PullRequest("flock-php", "flock", 87)
	if err != nil || pr.Body != "Fixes #73" {
		t.Fatalf("PullRequest() = %+v, %v", pr, err)
	}
	permission, err := client.Permission("flock-php", "flock", "contributor")
	if err != nil || permission != "read" {
		t.Fatalf("Permission() = %q, %v", permission, err)
	}
	files, err := client.Files("flock-php", "flock", 87)
	if err != nil || len(files) != 2 {
		t.Fatalf("Files() = %+v, %v", files, err)
	}
	issue, err := client.Issue("flock-php", "flock", 73)
	if err != nil || issue.Number != 73 {
		t.Fatalf("Issue() = %+v, %v", issue, err)
	}
	closed, err := client.GateClosed("flock-php", "flock", 87)
	if err != nil || !closed {
		t.Fatalf("GateClosed() = %t, %v", closed, err)
	}
	if err := client.Comment("flock-php", "flock", 87, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetPullRequestState("flock-php", "flock", 87, "closed"); err != nil {
		t.Fatal(err)
	}
	if err := client.AddLabel("flock-php", "flock", 73, "needs triage"); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(writes, "\n"); !strings.Contains(got, `{"body":"hello"}`) || !strings.Contains(got, `{"state":"closed"}`) || !strings.Contains(got, `{"labels":["needs triage"]}`) {
		t.Fatalf("writes = %s", got)
	}
}

func TestClientGateClosedUsesLatestClosureAndMarker(t *testing.T) {
	tests := []struct {
		name        string
		commentTime string
		events      string
		want        bool
	}{
		{
			name:        "gate closed after the latest reopen",
			commentTime: "2026-01-02T00:00:00Z",
			events:      `[{"event":"reopened","actor":{"login":"contributor"},"created_at":"2026-01-01T00:00:00Z"},{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-02T00:00:01Z"}]`,
			want:        true,
		},
		{
			name:        "manual closure after a gate reopen",
			commentTime: "2026-01-01T00:00:00Z",
			events:      `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"},{"event":"reopened","actor":{"login":"contributor"},"created_at":"2026-01-02T00:00:00Z"},{"event":"closed","actor":{"login":"maintainer"},"created_at":"2026-01-03T00:00:00Z"}]`,
			want:        false,
		},
		{
			name:        "bot closure without a fresh marker",
			commentTime: "2026-01-01T00:00:00Z",
			events:      `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"},{"event":"reopened","actor":{"login":"contributor"},"created_at":"2026-01-02T00:00:00Z"},{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-03T00:00:00Z"}]`,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/flock-php/flock/issues/87/comments":
					_, _ = io.WriteString(w, `[{"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:closed -->","created_at":"`+tt.commentTime+`"}]`)
				case "/repos/flock-php/flock/issues/87/events":
					_, _ = io.WriteString(w, tt.events)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			closed, err := NewClient(server.URL, "token").GateClosed("flock-php", "flock", 87)
			if err != nil || closed != tt.want {
				t.Fatalf("GateClosed() = %t, %v; want %t", closed, err, tt.want)
			}
		})
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
