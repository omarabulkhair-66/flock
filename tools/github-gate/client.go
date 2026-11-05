package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const gateMarker = "<!-- pr-gate:closed -->"

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	return &Client{baseURL: parsed, token: token, httpClient: http.DefaultClient}
}

func (c *Client) PullRequest(owner, repository string, number int) (PullRequest, error) {
	var pullRequest PullRequest
	err := c.getJSON(fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repository, number), &pullRequest)
	return pullRequest, err
}

func (c *Client) Permission(owner, repository, username string) (string, error) {
	var response struct {
		Permission string `json:"permission"`
	}
	err := c.getJSON(fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", owner, repository, username), &response)
	return response.Permission, err
}

func (c *Client) Files(owner, repository string, number int) ([]File, error) {
	var files []File
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files", owner, repository, number)
	for page := 1; ; page++ {
		var items []File
		next, err := c.getPage(path, page, &items)
		if err != nil {
			return nil, err
		}
		files = append(files, items...)
		if !next {
			return files, nil
		}
	}
}

func (c *Client) Issue(owner, repository string, number int) (Issue, error) {
	var issue Issue
	err := c.getJSON(fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repository, number), &issue)
	return issue, err
}

func (c *Client) GateClosed(owner, repository string, number int) (bool, error) {
	type comment struct {
		User      User      `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	commentsPath := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repository, number)
	var latestMarker time.Time
	for page := 1; ; page++ {
		var comments []comment
		next, err := c.getPage(commentsPath, page, &comments)
		if err != nil {
			return false, err
		}
		for _, comment := range comments {
			if comment.User.Login == "github-actions[bot]" && strings.Contains(comment.Body, gateMarker) && comment.CreatedAt.After(latestMarker) {
				latestMarker = comment.CreatedAt
			}
		}
		if !next {
			break
		}
	}
	if latestMarker.IsZero() {
		return false, nil
	}

	type event struct {
		Event     string    `json:"event"`
		Actor     User      `json:"actor"`
		CreatedAt time.Time `json:"created_at"`
	}
	eventsPath := fmt.Sprintf("/repos/%s/%s/issues/%d/events", owner, repository, number)
	var latestTransition event
	var latestReopen time.Time
	foundTransition := false
	for page := 1; ; page++ {
		var events []event
		next, err := c.getPage(eventsPath, page, &events)
		if err != nil {
			return false, err
		}
		for _, current := range events {
			if current.Event == "reopened" && current.CreatedAt.After(latestReopen) {
				latestReopen = current.CreatedAt
			}
			if (current.Event == "closed" || current.Event == "reopened") &&
				(!foundTransition || !current.CreatedAt.Before(latestTransition.CreatedAt)) {
				latestTransition = current
				foundTransition = true
			}
		}
		if !next {
			break
		}
	}

	return foundTransition &&
		latestTransition.Event == "closed" &&
		latestTransition.Actor.Login == "github-actions[bot]" &&
		!latestMarker.Before(latestReopen) &&
		!latestMarker.After(latestTransition.CreatedAt), nil
}

func (c *Client) Comment(owner, repository string, number int, body string) error {
	return c.sendJSON(http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repository, number), map[string]string{"body": body}, nil)
}

func (c *Client) SetPullRequestState(owner, repository string, number int, state string) error {
	return c.sendJSON(http.MethodPatch, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repository, number), map[string]string{"state": state}, nil)
}

func (c *Client) AddLabel(owner, repository string, number int, label string) error {
	return c.sendJSON(http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repository, number), map[string][]string{"labels": {label}}, nil)
}

func (c *Client) getJSON(path string, destination any) error {
	return c.sendJSON(http.MethodGet, path, nil, destination)
}

func (c *Client) getPage(path string, page int, destination any) (bool, error) {
	response, err := c.request(http.MethodGet, path+"?per_page=100&page="+strconv.Itoa(page), nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return false, err
	}
	return strings.Contains(response.Header.Get("Link"), `rel="next"`), nil
}

func (c *Client) sendJSON(method, path string, input, destination any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	response, err := c.request(method, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if destination == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(destination)
}

func (c *Client) request(method, path string, body io.Reader) (*http.Response, error) {
	endpoint, err := c.baseURL.Parse(path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		message, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("GitHub API %s %s: %s: %s", method, path, response.Status, strings.TrimSpace(string(message)))
	}
	return response, nil
}
