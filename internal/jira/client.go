/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"altech.com/operator/internal/policy"
)

// ErrNotFound is returned when JIRA responds with HTTP 404 for an issue.
var ErrNotFound = errors.New("jira issue not found")

// Issue is a minimal view of a JIRA issue for Cleaner rule JiraClosed.
type Issue struct {
	Key      string
	Done     bool
	ClosedAt time.Time
}

// ToPolicy converts to policy.JiraInfo with Found=true.
func (i Issue) ToPolicy() policy.JiraInfo {
	return policy.JiraInfo{
		Found:    true,
		Done:     i.Done,
		ClosedAt: i.ClosedAt,
	}
}

// Client talks to JIRA Server/DC/Cloud REST API v2.
type Client struct {
	baseURL    string
	username   string
	apiToken   string
	httpClient *http.Client
}

// NewClient builds a Client from credentials.
// httpClient may be nil (defaults to http.DefaultClient).
func NewClient(creds Credentials, httpClient *http.Client) (*Client, error) {
	if creds.URL == "" {
		return nil, fmt.Errorf("jira url is empty")
	}
	if creds.APIToken == "" {
		return nil, fmt.Errorf("jira apiToken is empty")
	}
	if _, err := url.ParseRequestURI(creds.URL); err != nil {
		return nil, fmt.Errorf("jira url is invalid: %w", err)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(creds.URL, "/"),
		username:   creds.Username,
		apiToken:   creds.APIToken,
		httpClient: httpClient,
	}, nil
}

// GetIssue fetches issue fields needed for the closed/grace check.
// Returns ErrNotFound on HTTP 404.
func (c *Client) GetIssue(ctx context.Context, issueKey string) (Issue, error) {
	issueKey = strings.TrimSpace(issueKey)
	if issueKey == "" {
		return Issue{}, fmt.Errorf("jira issue key is empty")
	}

	endpoint := fmt.Sprintf(
		"%s/rest/api/2/issue/%s?fields=status,resolutiondate,statuscategorychangedate",
		c.baseURL,
		url.PathEscape(issueKey),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Issue{}, fmt.Errorf("build jira request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Issue{}, fmt.Errorf("jira request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Issue{}, fmt.Errorf("read jira response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// continue
	case http.StatusNotFound:
		return Issue{}, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return Issue{}, fmt.Errorf("jira auth failed: status %d", resp.StatusCode)
	default:
		return Issue{}, fmt.Errorf("jira unexpected status %d", resp.StatusCode)
	}

	var raw issueResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return Issue{}, fmt.Errorf("decode jira response: %w", err)
	}

	issue := Issue{Key: issueKey}
	if raw.Key != "" {
		issue.Key = raw.Key
	}
	issue.Done = strings.EqualFold(raw.Fields.Status.StatusCategory.Key, "done")
	if raw.Fields.ResolutionDate != nil {
		issue.ClosedAt = parseJiraTime(*raw.Fields.ResolutionDate)
	}
	if issue.ClosedAt.IsZero() && raw.Fields.StatusCategoryChangeDate != nil {
		issue.ClosedAt = parseJiraTime(*raw.Fields.StatusCategoryChangeDate)
	}
	return issue, nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.username != "" {
		req.SetBasicAuth(c.username, c.apiToken)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
}

type issueResponse struct {
	Key    string `json:"key"`
	Fields struct {
		Status struct {
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		// JIRA may send null for these fields; *string accepts null as nil.
		ResolutionDate           *string `json:"resolutiondate"`
		StatusCategoryChangeDate *string `json:"statuscategorychangedate"`
	} `json:"fields"`
}

// JIRA timestamps look like 2024-01-02T15:04:05.000+0000 or with Z / milliseconds variants.
func parseJiraTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.000Z0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
