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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCredentialsFromSecret(t *testing.T) {
	t.Parallel()

	t.Run("ok with username", func(t *testing.T) {
		t.Parallel()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jira-credentials"},
			Data: map[string][]byte{
				SecretKeyURL:      []byte("https://jira.example.com/"),
				SecretKeyUsername: []byte("bot"),
				SecretKeyAPIToken: []byte("token-value"),
			},
		}
		creds, err := CredentialsFromSecret(secret)
		if err != nil {
			t.Fatal(err)
		}
		if creds.URL != "https://jira.example.com" {
			t.Fatalf("URL = %q", creds.URL)
		}
		if creds.Username != "bot" || creds.APIToken != "token-value" {
			t.Fatalf("unexpected creds: %+v", creds)
		}
	})

	t.Run("ok without username", func(t *testing.T) {
		t.Parallel()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jira-credentials"},
			Data: map[string][]byte{
				SecretKeyURL:      []byte("https://jira.example.com"),
				SecretKeyAPIToken: []byte("token-value"),
			},
		}
		creds, err := CredentialsFromSecret(secret)
		if err != nil {
			t.Fatal(err)
		}
		if creds.Username != "" {
			t.Fatalf("username = %q", creds.Username)
		}
	})

	t.Run("missing url", func(t *testing.T) {
		t.Parallel()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jira-credentials"},
			Data: map[string][]byte{
				SecretKeyAPIToken: []byte("token-value"),
			},
		}
		_, err := CredentialsFromSecret(secret)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), "token-value") {
			t.Fatalf("error leaked secret: %v", err)
		}
	})

	t.Run("missing token", func(t *testing.T) {
		t.Parallel()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "jira-credentials"},
			Data: map[string][]byte{
				SecretKeyURL: []byte("https://jira.example.com"),
			},
		}
		_, err := CredentialsFromSecret(secret)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("nil secret", func(t *testing.T) {
		t.Parallel()
		_, err := CredentialsFromSecret(nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
