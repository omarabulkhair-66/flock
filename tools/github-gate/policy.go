package main

import (
	"fmt"
	"regexp"
	"strings"
)

const openToAnyone = "help wanted"

type Action string

const (
	Allow           Action = "allow"
	CloseWrongBase  Action = "close-wrong-base"
	CloseUnapproved Action = "close-unapproved"
)

type Decision struct {
	Action Action
	Issue  IssueReference
}

type User struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type Branch struct {
	Ref string `json:"ref"`
}

type PullRequest struct {
	Number            int    `json:"number"`
	State             string `json:"state"`
	Merged            bool   `json:"merged"`
	Body              string `json:"body"`
	User              User   `json:"user"`
	AuthorAssociation string `json:"author_association"`
	Base              Branch `json:"base"`
}

type File struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
}

type Label struct {
	Name string `json:"name"`
}

type Issue struct {
	Number            int       `json:"number"`
	State             string    `json:"state"`
	User              User      `json:"user"`
	AuthorAssociation string    `json:"author_association"`
	Labels            []Label   `json:"labels"`
	Assignees         []User    `json:"assignees"`
	PullRequest       *struct{} `json:"pull_request"`
}

type IssueReference struct {
	Repository string
	Number     int
}

func (reference IssueReference) String() string {
	return fmt.Sprintf("%s#%d", reference.Repository, reference.Number)
}

var (
	shortIssueReference = regexp.MustCompile(`(?:^|[^\w/])#(\d+)\b`)
	fullIssueReference  = regexp.MustCompile(`(?i)flock-php/(flock|binaries)(?:#|/issues/)(\d+)\b`)
)

func EvaluatePullRequest(pr PullRequest, permission string, files []File, issues map[IssueReference]Issue) Decision {
	if bypassesGate(pr, permission) {
		return Decision{Action: Allow}
	}
	if pr.Base.Ref != "main" {
		return Decision{Action: CloseWrongBase}
	}
	if docsOnly(files) {
		return Decision{Action: Allow}
	}
	for _, reference := range IssueReferences(pr.Body, "flock") {
		if issue, ok := issues[reference]; ok && isApproved(issue, pr.User.Login) {
			return Decision{Action: Allow, Issue: reference}
		}
	}
	return Decision{Action: CloseUnapproved}
}

func MayReopen(pr PullRequest, bodyEdited, closedByGate, approved bool) bool {
	return bodyEdited &&
		closedByGate &&
		approved &&
		pr.State == "closed" &&
		!pr.Merged &&
		pr.Base.Ref == "main"
}

func ShouldTriageIssue(issue Issue, permission string) bool {
	return !bypassesGate(PullRequest{
		User:              issue.User,
		AuthorAssociation: issue.AuthorAssociation,
	}, permission)
}

func IssueReferences(body, repository string) []IssueReference {
	refs := make(map[IssueReference]bool)
	for _, match := range shortIssueReference.FindAllStringSubmatch(body, -1) {
		refs[IssueReference{Repository: repository, Number: parseNumber(match[1])}] = true
	}
	for _, match := range fullIssueReference.FindAllStringSubmatch(body, -1) {
		refs[IssueReference{Repository: strings.ToLower(match[1]), Number: parseNumber(match[2])}] = true
	}

	result := make([]IssueReference, 0, len(refs))
	for reference := range refs {
		result = append(result, reference)
	}
	return result
}

func bypassesGate(pr PullRequest, permission string) bool {
	return pr.User.Type == "Bot" ||
		pr.AuthorAssociation == "OWNER" ||
		pr.AuthorAssociation == "MEMBER" ||
		pr.AuthorAssociation == "COLLABORATOR" ||
		permission == "admin" || permission == "write"
}

func docsOnly(files []File) bool {
	for _, file := range files {
		if !strings.HasPrefix(file.Filename, "docs/") ||
			(file.PreviousFilename != "" && !strings.HasPrefix(file.PreviousFilename, "docs/")) {
			return false
		}
	}
	return true
}

func isApproved(issue Issue, author string) bool {
	if issue.PullRequest != nil || issue.State != "open" {
		return false
	}
	for _, label := range issue.Labels {
		if label.Name == openToAnyone {
			return true
		}
	}
	for _, assignee := range issue.Assignees {
		if assignee.Login == author {
			return true
		}
	}
	return false
}

func parseNumber(value string) int {
	number := 0
	for _, digit := range value {
		number = number*10 + int(digit-'0')
	}
	return number
}
