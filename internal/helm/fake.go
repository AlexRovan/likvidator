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
	"context"
	"fmt"
	"sync"
)

// Fake is an in-memory Helm client for tests.
type Fake struct {
	mu          sync.Mutex
	Releases    map[string]ReleaseInfo // key: namespace/name
	Uninstalled []string               // "namespace/name"
}

func releaseKey(name, namespace string) string {
	return namespace + "/" + name
}

// Get implements Interface.
func (f *Fake) Get(_ context.Context, name, namespace string) (ReleaseInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Releases == nil {
		return ReleaseInfo{}, ErrNotFound
	}
	info, ok := f.Releases[releaseKey(name, namespace)]
	if !ok {
		return ReleaseInfo{}, ErrNotFound
	}
	return info, nil
}

// Uninstall implements Interface.
func (f *Fake) Uninstall(_ context.Context, name, namespace string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := releaseKey(name, namespace)
	if f.Releases == nil {
		return ErrNotFound
	}
	if _, ok := f.Releases[key]; !ok {
		return ErrNotFound
	}
	delete(f.Releases, key)
	f.Uninstalled = append(f.Uninstalled, key)
	return nil
}

// SetRelease stores a release for tests.
func (f *Fake) SetRelease(info ReleaseInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Releases == nil {
		f.Releases = map[string]ReleaseInfo{}
	}
	if info.Name == "" || info.Namespace == "" {
		panic(fmt.Sprintf("invalid release info: %+v", info))
	}
	f.Releases[releaseKey(info.Name, info.Namespace)] = info
}
