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

package policy

import (
	"regexp"
	"strings"
)

// Image tag format: <PROJECT>-<NUMBER>-<HASH>, e.g. PROJ-123-abc12.
// Capture group 1 is the JIRA issue key.
var imageTagPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]+-\d+)-([A-Za-z0-9]+)$`)

// ParseImageTag extracts the JIRA issue key from a container image tag.
// The tag is the substring after the last ":" in image (Docker/OCI ref without digest).
// Returns ok=false when the tag does not match PROJ-123-hash.
func ParseImageTag(image string) (issueKey string, ok bool) {
	tag := imageTag(image)
	if tag == "" {
		return "", false
	}
	matches := imageTagPattern.FindStringSubmatch(tag)
	if len(matches) != 3 {
		return "", false
	}
	return matches[1], true
}

// ParseIssueKeyFromImages returns the first issue key parsed from any image.
func ParseIssueKeyFromImages(images ...string) (issueKey string, ok bool) {
	for _, image := range images {
		if key, found := ParseImageTag(image); found {
			return key, true
		}
	}
	return "", false
}

func imageTag(image string) string {
	if image == "" {
		return ""
	}
	// Digests use "@"; tags use ":". Prefer last ":" that is not part of a registry port
	// only when there is no "@sha256" style digest after a tag — keep it simple:
	// take substring after the last ":" if it does not contain "/".
	if i := strings.LastIndex(image, "@"); i >= 0 {
		image = image[:i]
	}
	i := strings.LastIndex(image, ":")
	if i < 0 || i == len(image)-1 {
		return ""
	}
	tag := image[i+1:]
	if strings.Contains(tag, "/") {
		// "registry:5000/repo" without tag
		return ""
	}
	return tag
}
