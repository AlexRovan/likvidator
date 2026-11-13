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

import "testing"

func TestParseImageTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		image string
		key   string
		ok    bool
	}{
		{"registry.example.com/demo:PROJ-123-abc12", "PROJ-123", true},
		{"demo:ABC-1-deadbeef", "ABC-1", true},
		{"demo:PROJ-123-abc12", "PROJ-123", true},
		{"demo:PROJ-123-abc12@sha256:deadbeef", "PROJ-123", true},
		{"demo:latest", "", false},
		{"demo:v1.2.3", "", false},
		{"demo:task-123-abc", "", false}, // project must be uppercase
		{"demo:123-abc", "", false},
		{"demo:PROJ-123", "", false}, // missing hash segment
		{"registry.example.com/demo", "", false},
		{"registry:5000/demo", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			t.Parallel()
			key, ok := ParseImageTag(tt.image)
			if ok != tt.ok || key != tt.key {
				t.Fatalf("ParseImageTag(%q) = (%q, %v), want (%q, %v)", tt.image, key, ok, tt.key, tt.ok)
			}
		})
	}
}

func TestParseIssueKeyFromImages(t *testing.T) {
	t.Parallel()

	key, ok := ParseIssueKeyFromImages("nginx:latest", "app:PROJ-9-ffff")
	if !ok || key != "PROJ-9" {
		t.Fatalf("got (%q, %v), want (PROJ-9, true)", key, ok)
	}
}
