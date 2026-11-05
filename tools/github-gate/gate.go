package main

import (
	"fmt"
	"strings"
	"time"
)

type GitHub interface {
	PullRequest(owner, repository string, number int) (PullRequest, error)
	Permission(owner, repository, username string) (string, error)
	Files(owner, repository string, number int) ([]File, error)
	Issue(owner, repository string, number int) (Issue, error)
	GateClosed(owner, repository string, number int) (bool, error)
	Comment(owner, repository string, number int, body string) error
	SetPullRequestState(owner, repository string, number int, state string) error
	AddLabel(owner, repository string, number int, label string) error
}

type PullRequestEvent struct {
	Action     string
	Number     int
	BodyEdited bool
	BaseEdited bool
}

func RunPRGate(api GitHub, owner, repository string, event PullRequestEvent) error {
	for attempt := 0; ; attempt++ {
		pr, err := api.PullRequest(owner, repository, event.Number)
		if err != nil {
			return err
		}
		permission, err := api.Permission(owner, repository, pr.User.Login)
		if err != nil {
			return err
		}
		if bypassesGate(pr, permission) {
			return nil
		}

		if pr.State == "closed" {
			if !event.BodyEdited || pr.Merged || pr.Base.Ref != "main" {
				return nil
			}
			closedByGate, err := api.GateClosed(owner, repository, pr.Number)
			if err != nil {
				return err
			}
			if !closedByGate {
				return nil
			}
			if issue, approved := firstApprovedIssue(api, owner, pr); MayReopen(pr, event.BodyEdited, closedByGate, approved) {
				if err := api.SetPullRequestState(owner, repository, pr.Number, "open"); err != nil {
					return err
				}
				return api.Comment(owner, repository, pr.Number, fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", pr.User.Login, issue))
			}
			return nil
		}

		if event.Action == "edited" && !event.BaseEdited {
			return nil
		}
		files, err := api.Files(owner, repository, pr.Number)
		if err != nil {
			return err
		}
		decision := EvaluatePullRequest(pr, permission, files, nil)
		if decision.Action == CloseUnapproved {
			if issue, approved := firstApprovedIssue(api, owner, pr); approved {
				decision = Decision{Action: Allow, Issue: issue}
			}
		}
		if decision.Action == Allow {
			return nil
		}
		if decision.Action == CloseUnapproved && event.Action == "opened" && attempt < 2 {
			time.Sleep(3 * time.Second)
			continue
		}
		return closePullRequest(api, owner, repository, pr, decision)
	}
}

func RunIssueTriage(api GitHub, owner, repository string, issue Issue) error {
	permission, err := api.Permission(owner, repository, issue.User.Login)
	if err != nil {
		return err
	}
	if !ShouldTriageIssue(issue, permission) {
		return nil
	}
	return api.AddLabel(owner, repository, issue.Number, "needs triage")
}

func firstApprovedIssue(api GitHub, owner string, pr PullRequest) (IssueReference, bool) {
	for _, reference := range IssueReferences(pr.Body, "flock") {
		issue, err := api.Issue(owner, reference.Repository, reference.Number)
		if err == nil && isApproved(issue, pr.User.Login) {
			return reference, true
		}
	}
	return IssueReference{}, false
}

func closePullRequest(api GitHub, owner, repository string, pr PullRequest, decision Decision) error {
	var reason, nextStep string
	switch decision.Action {
	case CloseWrongBase:
		reason = fmt.Sprintf("Pull requests go into `main`, not `%s`, so this one is closed.", pr.Base.Ref)
		nextStep = "Please open a new pull request against `main`."
	case CloseUnapproved:
		reason = "Flock takes pull requests only for issues a maintainer has approved, so this one is closed."
		nextStep = "Edit this pull request's description to reference an approved issue, then save it to reopen automatically."
	default:
		return fmt.Errorf("cannot close a pull request for %q", decision.Action)
	}
	comment := fmt.Sprintf("Thanks for the PR, @%s! %s %s See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).\n\n%s", pr.User.Login, reason, nextStep, owner, repository, gateMarker)
	if err := api.Comment(owner, repository, pr.Number, comment); err != nil {
		return err
	}
	return api.SetPullRequestState(owner, repository, pr.Number, "closed")
}

func repositoryParts(repository string) (string, string, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be owner/repository, got %q", repository)
	}
	return owner, name, nil
}
