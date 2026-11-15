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
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Secret data keys required by SPEC §4.3.
const (
	SecretKeyURL      = "url"
	SecretKeyUsername = "username"
	SecretKeyAPIToken = "apiToken"
)

// Credentials holds JIRA connection settings loaded from a Kubernetes Secret.
// Never log Username or APIToken.
type Credentials struct {
	URL      string
	Username string
	APIToken string
}

// CredentialsFromSecret reads url, apiToken, and optional username from secret.Data.
// Values must not appear in returned error strings.
func CredentialsFromSecret(secret *corev1.Secret) (Credentials, error) {
	if secret == nil {
		return Credentials{}, fmt.Errorf("jira secret is nil")
	}

	url := strings.TrimSpace(string(secret.Data[SecretKeyURL]))
	token := strings.TrimSpace(string(secret.Data[SecretKeyAPIToken]))
	username := strings.TrimSpace(string(secret.Data[SecretKeyUsername]))

	if url == "" {
		return Credentials{}, fmt.Errorf("jira secret %q missing required key %q", secret.Name, SecretKeyURL)
	}
	if token == "" {
		return Credentials{}, fmt.Errorf("jira secret %q missing required key %q", secret.Name, SecretKeyAPIToken)
	}

	return Credentials{
		URL:      strings.TrimRight(url, "/"),
		Username: username,
		APIToken: token,
	}, nil
}
