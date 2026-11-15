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

package helm

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/release"
	helmtime "helm.sh/helm/v3/pkg/time"
)

func TestDecodeRelease(t *testing.T) {
	t.Parallel()

	deployed := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rel := &release.Release{
		Name:      "demo",
		Namespace: "default",
		Version:   1,
		Info: &release.Info{
			LastDeployed: helmtime.Time{Time: deployed},
		},
	}
	raw, err := json.Marshal(rel)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	got, err := decodeRelease(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "demo" || got.Namespace != "default" {
		t.Fatalf("got %+v", got)
	}
	if !got.Info.LastDeployed.Time.Equal(deployed) {
		t.Fatalf("LastDeployed = %v", got.Info.LastDeployed)
	}
}

func TestDecodeReleaseEmpty(t *testing.T) {
	t.Parallel()
	if _, err := decodeRelease(""); err == nil {
		t.Fatal("expected error")
	}
}
