package main

import "testing"

func TestEvaluatePullRequest(t *testing.T) {
	outside := User{Login: "contributor", Type: "User"}
	approved := Issue{Number: 73, State: "open", Labels: []Label{{Name: openToAnyone}}}
	assigned := Issue{Number: 74, State: "open", Assignees: []User{outside}}

	tests := []struct {
		name   string
		pr     PullRequest
		access string
		files  []File
		issues map[IssueReference]Issue
		want   Decision
	}{
		{
			name:   "collaborator bypasses the gate",
			pr:     PullRequest{User: outside, AuthorAssociation: "COLLABORATOR", State: "open", Base: Branch{Ref: "main"}},
			access: "read",
			want:   Decision{Action: Allow},
		},
		{
			name:   "writer bypasses the gate",
			pr:     PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}},
			access: "write",
			want:   Decision{Action: Allow},
		},
		{
			name:  "wrong base is closed",
			pr:    PullRequest{User: outside, State: "open", Base: Branch{Ref: "release"}},
			files: []File{{Filename: "docs/sites.md"}},
			want:  Decision{Action: CloseWrongBase},
		},
		{
			name: "an empty file list is docs only",
			pr:   PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}},
			want: Decision{Action: Allow},
		},
		{
			name:  "docs only needs no issue",
			pr:    PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}},
			files: []File{{Filename: "docs/sites.md"}},
			want:  Decision{Action: Allow},
		},
		{
			name:   "help wanted issue allows the pull request",
			pr:     PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #73"},
			files:  []File{{Filename: "internal/site.go"}},
			issues: map[IssueReference]Issue{{Repository: "flock", Number: 73}: approved},
			want:   Decision{Action: Allow, Issue: IssueReference{Repository: "flock", Number: 73}},
		},
		{
			name:   "assigned issue allows its contributor",
			pr:     PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #74"},
			files:  []File{{Filename: "internal/site.go"}},
			issues: map[IssueReference]Issue{{Repository: "flock", Number: 74}: assigned},
			want:   Decision{Action: Allow, Issue: IssueReference{Repository: "flock", Number: 74}},
		},
		{
			name:   "unapproved code pull request is closed",
			pr:     PullRequest{User: outside, State: "open", Base: Branch{Ref: "main"}, Body: "Fixes #75"},
			files:  []File{{Filename: "internal/site.go"}},
			issues: map[IssueReference]Issue{{Repository: "flock", Number: 75}: {Number: 75, State: "open"}},
			want:   Decision{Action: CloseUnapproved},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluatePullRequest(tt.pr, tt.access, tt.files, tt.issues)
			if got != tt.want {
				t.Fatalf("EvaluatePullRequest() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMayReopen(t *testing.T) {
	pr := PullRequest{State: "closed", Base: Branch{Ref: "main"}}

	tests := []struct {
		name     string
		pr       PullRequest
		bodyEdit bool
		gate     bool
		approved bool
		want     bool
	}{
		{"gate closed approved body edit", pr, true, true, true, true},
		{"manually closed", pr, true, false, true, false},
		{"merged", PullRequest{State: "closed", Merged: true, Base: Branch{Ref: "main"}}, true, true, true, false},
		{"wrong base", PullRequest{State: "closed", Base: Branch{Ref: "release"}}, true, true, true, false},
		{"title edit", pr, false, true, true, false},
		{"still unapproved", pr, true, true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MayReopen(tt.pr, tt.bodyEdit, tt.gate, tt.approved); got != tt.want {
				t.Fatalf("MayReopen() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestShouldTriageIssue(t *testing.T) {
	if !ShouldTriageIssue(Issue{User: User{Login: "contributor", Type: "User"}}, "read") {
		t.Fatal("outside contributor should need triage")
	}
	if ShouldTriageIssue(Issue{User: User{Login: "maintainer", Type: "User"}, AuthorAssociation: "MEMBER"}, "read") {
		t.Fatal("member should not need triage")
	}
	if ShouldTriageIssue(Issue{User: User{Login: "writer", Type: "User"}}, "write") {
		t.Fatal("writer should not need triage")
	}
}
