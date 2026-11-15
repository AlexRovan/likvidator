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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/releaseutil"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ErrNotFound is returned when a Helm release is missing from storage.
var ErrNotFound = errors.New("helm release not found")

const (
	secretTypeHelmV1 = "helm.sh/release.v1"
	labelOwner     = "owner"
	labelOwnerHelm = "helm"
	labelName      = "name"
	labelVersion   = "version"
	dataReleaseKey = "release"
)

// ReleaseInfo is the subset of a Helm release needed by Cleaner.
type ReleaseInfo struct {
	Name         string
	Namespace    string
	LastDeployed time.Time
}

// Interface gets and uninstalls Helm releases.
type Interface interface {
	Get(ctx context.Context, name, namespace string) (ReleaseInfo, error)
	Uninstall(ctx context.Context, name, namespace string) error
}

// Client reads Helm release Secrets (driver=secret) via the Kubernetes API
// and uninstalls by deleting release resources then storage Secrets.
type Client struct {
	client client.Client
}

// NewClient builds a Helm storage client.
func NewClient(c client.Client) (*Client, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is nil")
	}
	return &Client{client: c}, nil
}

// Get returns release info including info.last_deployed from the latest Secret revision.
func (c *Client) Get(ctx context.Context, name, namespace string) (ReleaseInfo, error) {
	rel, _, err := c.latestRelease(ctx, name, namespace)
	if err != nil {
		return ReleaseInfo{}, err
	}
	info := ReleaseInfo{Name: rel.Name, Namespace: rel.Namespace}
	if rel.Info != nil {
		info.LastDeployed = rel.Info.LastDeployed.Time
	}
	return info, nil
}

// Uninstall deletes resources from the release manifest, then Helm storage Secrets.
func (c *Client) Uninstall(ctx context.Context, name, namespace string) error {
	rel, secrets, err := c.latestRelease(ctx, name, namespace)
	if err != nil {
		return err
	}

	if rel.Manifest != "" {
		if err := c.deleteManifestResources(ctx, rel.Manifest, namespace); err != nil {
			return err
		}
	}

	var errs []error
	for i := range secrets {
		if err := c.client.Delete(ctx, &secrets[i]); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Client) latestRelease(ctx context.Context, name, namespace string) (*release.Release, []corev1.Secret, error) {
	var list corev1.SecretList
	if err := c.client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{
			labelOwner: labelOwnerHelm,
			labelName:  name,
		},
	); err != nil {
		return nil, nil, fmt.Errorf("list helm secrets for %q in %q: %w", name, namespace, err)
	}
	if len(list.Items) == 0 {
		return nil, nil, ErrNotFound
	}

	var (
		best     *release.Release
		bestVer  int
		bestList []corev1.Secret
	)
	for i := range list.Items {
		sec := list.Items[i]
		if sec.Type != "" && sec.Type != secretTypeHelmV1 {
			continue
		}
		rel, err := decodeRelease(string(sec.Data[dataReleaseKey]))
		if err != nil {
			continue
		}
		ver := rel.Version
		if vLabel := sec.Labels[labelVersion]; vLabel != "" {
			if parsed, err := strconv.Atoi(vLabel); err == nil {
				ver = parsed
			}
		}
		if best == nil || ver >= bestVer {
			best = rel
			bestVer = ver
			bestList = list.Items
		}
	}
	if best == nil {
		return nil, nil, ErrNotFound
	}
	return best, bestList, nil
}

func (c *Client) deleteManifestResources(ctx context.Context, manifest, namespace string) error {
	files := releaseutil.SplitManifests(manifest)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(releaseutil.BySplitManifestsOrder(keys)))

	var errs []error
	for _, k := range keys {
		doc := strings.TrimSpace(files[k])
		if doc == "" || doc == "---" {
			continue
		}
		obj, err := decodeUnstructured(doc)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if obj.GetNamespace() == "" && isNamespaced(obj) {
			obj.SetNamespace(namespace)
		}
		if err := c.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete %s %s/%s: %w", obj.GroupVersionKind().String(), obj.GetNamespace(), obj.GetName(), err))
		}
	}
	return errors.Join(errs...)
}

func decodeUnstructured(doc string) (*unstructured.Unstructured, error) {
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(doc)), 4096)
	obj := &unstructured.Unstructured{}
	if err := dec.Decode(obj); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if obj.GetKind() == "" || obj.GetAPIVersion() == "" {
		return nil, fmt.Errorf("manifest missing kind/apiVersion")
	}
	return obj, nil
}

func isNamespaced(obj *unstructured.Unstructured) bool {
	switch obj.GetKind() {
	case "Namespace", "ClusterRole", "ClusterRoleBinding", "PersistentVolume",
		"StorageClass", "CustomResourceDefinition", "PriorityClass", "IngressClass":
		return false
	default:
		return true
	}
}

// decodeRelease mirrors helm storage driver decoding: base64(+gzip)+json.
func decodeRelease(data string) (*release.Release, error) {
	if data == "" {
		return nil, fmt.Errorf("empty release payload")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	// Helm may skip gzip for tiny payloads; detect gzip magic.
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		b, err = io.ReadAll(r)
		if err != nil {
			return nil, err
		}
	}
	var rel release.Release
	if err := json.Unmarshal(b, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}
