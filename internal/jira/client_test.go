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
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	t.Parallel()

	_, err := NewClient(Credentials{URL: "https://jira.example.com", APIToken: "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewClient(Credentials{URL: "not-a-url", APIToken: "t"}, nil)
	if err == nil {
		t.Fatal("expected invalid url error")
	}
}

func TestGetIssueDoneWithResolutionDate(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/rest/api/2/issue/PROJ-123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.RawQuery, "fields=") {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"key": "PROJ-123",
			"fields": {
				"status": {"statusCategory": {"key": "done"}},
				"resolutiondate": "2026-11-10T12:00:00.000+0000",
				"statuscategorychangedate": "2026-11-09T12:00:00.000+0000"
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Credentials{
		URL:      srv.URL,
		Username: "bot",
		APIToken: "secret-token",
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	issue, err := client.GetIssue(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if !issue.Done || issue.Key != "PROJ-123" {
		t.Fatalf("issue = %+v", issue)
	}
	wantClosed := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if !issue.ClosedAt.Equal(wantClosed) {
		t.Fatalf("ClosedAt = %v, want %v", issue.ClosedAt, wantClosed)
	}

	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:secret-token"))
	if gotAuth != wantBasic {
		t.Fatalf("Authorization = %q, want basic auth", gotAuth)
	}

	info := issue.ToPolicy()
	if !info.Found || !info.Done || !info.ClosedAt.Equal(wantClosed) {
		t.Fatalf("ToPolicy = %+v", info)
	}
}

func TestGetIssueBearerAuthAndFallbackClosedAt(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"key": "ABC-1",
			"fields": {
				"status": {"statusCategory": {"key": "done"}},
				"resolutiondate": "",
				"statuscategorychangedate": "2026-11-11T08:30:00.000+0000"
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Credentials{
		URL:      srv.URL,
		APIToken: "bearer-token",
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	issue, err := client.GetIssue(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer bearer-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	want := time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC)
	if !issue.ClosedAt.Equal(want) {
		t.Fatalf("ClosedAt = %v, want %v", issue.ClosedAt, want)
	}
}

func TestGetIssueNotDone(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"key": "PROJ-1",
			"fields": {
				"status": {"statusCategory": {"key": "indeterminate"}},
				"resolutiondate": null,
				"statuscategorychangedate": "2026-11-11T08:30:00.000+0000"
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Credentials{URL: srv.URL, APIToken: "t"}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	issue, err := client.GetIssue(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Done {
		t.Fatalf("expected not done: %+v", issue)
	}
}

func TestGetIssueNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Credentials{URL: srv.URL, APIToken: "t"}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetIssue(context.Background(), "MISSING-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetIssueAuthFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorMessages":["Unauthorized"]}`))
	}))
	t.Cleanup(srv.Close)

	client, err := NewClient(Credentials{URL: srv.URL, APIToken: "bad"}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetIssue(context.Background(), "PROJ-1")
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "bad") {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestParseJiraTime(t *testing.T) {
	t.Parallel()

	got := parseJiraTime("2026-11-10T12:00:00.000+0000")
	want := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if !parseJiraTime("").IsZero() {
		t.Fatal("empty should be zero")
	}
}
