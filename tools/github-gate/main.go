// Command github-gate applies Flock's pull request and issue-triage policy in GitHub Actions.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "pr" && os.Args[1] != "issue") {
		fail(errors.New("usage: github-gate pr | issue"))
	}
	owner, repository, err := repositoryParts(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		fail(err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fail(errors.New("GITHUB_TOKEN is not set"))
	}
	event, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fail(err)
	}
	client := NewClient(apiURL(), token)

	switch os.Args[1] {
	case "pr":
		err = runPullRequestEvent(client, owner, repository, event)
	case "issue":
		err = runIssueEvent(client, owner, repository, event)
	}
	if err != nil {
		fail(err)
	}
}

func runPullRequestEvent(api GitHub, owner, repository string, data []byte) error {
	var event struct {
		Action      string `json:"action"`
		PullRequest struct {
			Number int `json:"number"`
		} `json:"pull_request"`
		Changes struct {
			Body *json.RawMessage `json:"body"`
			Base *json.RawMessage `json:"base"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	return RunPRGate(api, owner, repository, PullRequestEvent{
		Action:     event.Action,
		Number:     event.PullRequest.Number,
		BodyEdited: event.Changes.Body != nil,
		BaseEdited: event.Changes.Base != nil,
	})
}

func runIssueEvent(api GitHub, owner, repository string, data []byte) error {
	var event struct {
		Issue Issue `json:"issue"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	return RunIssueTriage(api, owner, repository, event.Issue)
}

func apiURL() string {
	if value := os.Getenv("GITHUB_API_URL"); value != "" {
		return value
	}
	return "https://api.github.com"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "github-gate:", err)
	os.Exit(1)
}
