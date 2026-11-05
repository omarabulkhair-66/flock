package main

import "testing"

func TestRunPRGateReopensOnlyGateClosedPullRequests(t *testing.T) {
	basePR := PullRequest{
		Number: 87, State: "closed", Base: Branch{Ref: "main"}, Body: "Fixes #73",
		User: User{Login: "contributor", Type: "User"},
	}

	tests := []struct {
		name        string
		pr          PullRequest
		gateClosed  bool
		wantState   string
		wantLookups int
	}{
		{"reopens a gate-closed pull request", basePR, true, "open", 1},
		{"does not reopen a manually closed pull request", basePR, false, "", 0},
		{"does not reopen a merged pull request", PullRequest{Number: 87, State: "closed", Merged: true, Base: Branch{Ref: "main"}, Body: "Fixes #73", User: basePR.User}, true, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeGitHub{
				pullRequest: tt.pr,
				issues: map[IssueReference]Issue{
					{Repository: "flock", Number: 73}: {Number: 73, State: "open", Labels: []Label{{Name: openToAnyone}}},
				},
				gateClosed: tt.gateClosed,
			}
			err := RunPRGate(api, "flock-php", "flock", PullRequestEvent{Action: "edited", Number: 87, BodyEdited: true})
			if err != nil {
				t.Fatal(err)
			}
			if api.state != tt.wantState {
				t.Fatalf("state = %q, want %q", api.state, tt.wantState)
			}
			if api.issueLookups != tt.wantLookups {
				t.Fatalf("issue lookups = %d, want %d", api.issueLookups, tt.wantLookups)
			}
		})
	}
}

func TestRunPRGateDefersAndShortCircuitsIssueLookups(t *testing.T) {
	approved := Issue{State: "open", Labels: []Label{{Name: openToAnyone}}}
	tests := []struct {
		name        string
		pr          PullRequest
		event       PullRequestEvent
		files       []File
		issues      map[IssueReference]Issue
		wantLookups int
	}{
		{
			name:        "title edit",
			pr:          PullRequest{State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #73", User: User{Login: "contributor"}},
			event:       PullRequestEvent{Action: "edited"},
			wantLookups: 0,
		},
		{
			name:        "wrong base",
			pr:          PullRequest{State: "open", Base: Branch{Ref: "release"}, Body: "Fixes #73", User: User{Login: "contributor"}},
			event:       PullRequestEvent{Action: "opened"},
			wantLookups: 0,
		},
		{
			name:        "docs only",
			pr:          PullRequest{State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #73", User: User{Login: "contributor"}},
			event:       PullRequestEvent{Action: "opened"},
			files:       []File{{Filename: "docs/readme.md"}},
			wantLookups: 0,
		},
		{
			name:  "stops after the first approved issue",
			pr:    PullRequest{State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #73 and #74", User: User{Login: "contributor"}},
			event: PullRequestEvent{Action: "opened"},
			issues: map[IssueReference]Issue{
				{Repository: "flock", Number: 73}: approved,
				{Repository: "flock", Number: 74}: approved,
			},
			wantLookups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeGitHub{pullRequest: tt.pr, files: tt.files, issues: tt.issues}
			if err := RunPRGate(api, "flock-php", "flock", tt.event); err != nil {
				t.Fatal(err)
			}
			if api.issueLookups != tt.wantLookups {
				t.Fatalf("issue lookups = %d, want %d", api.issueLookups, tt.wantLookups)
			}
		})
	}
}

type fakeGitHub struct {
	pullRequest  PullRequest
	files        []File
	issues       map[IssueReference]Issue
	gateClosed   bool
	state        string
	issueLookups int
}

func (f *fakeGitHub) PullRequest(string, string, int) (PullRequest, error) { return f.pullRequest, nil }
func (f *fakeGitHub) Permission(string, string, string) (string, error)    { return "read", nil }
func (f *fakeGitHub) Files(string, string, int) ([]File, error) {
	if f.files != nil {
		return f.files, nil
	}
	return []File{{Filename: "internal/site.go"}}, nil
}
func (f *fakeGitHub) Issue(_ string, repository string, number int) (Issue, error) {
	f.issueLookups++
	return f.issues[IssueReference{Repository: repository, Number: number}], nil
}
func (f *fakeGitHub) GateClosed(string, string, int) (bool, error) { return f.gateClosed, nil }
func (f *fakeGitHub) Comment(string, string, int, string) error    { return nil }
func (f *fakeGitHub) SetPullRequestState(_ string, _ string, _ int, state string) error {
	f.state = state
	return nil
}
func (f *fakeGitHub) AddLabel(string, string, int, string) error { return nil }
