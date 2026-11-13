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
	"time"

	appsv1 "altech.com/operator/api/v1"
	"altech.com/operator/internal/podutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JiraInfo is external JIRA state for a parsed issue key (filled by the reconciler).
type JiraInfo struct {
	// Found is false when the issue was not looked up or lookup failed.
	Found bool
	// Done is true when statusCategory.key == "done".
	Done bool
	// ClosedAt is resolutiondate or statuscategorychangedate.
	ClosedAt time.Time
}

// HelmInfo is external Helm release state for rule StaleRelease.
type HelmInfo struct {
	// Found is false when the release is missing from Helm storage.
	Found bool
	// LastDeployed is info.last_deployed.
	LastDeployed time.Time
}

// Thresholds are duration knobs from CleanerSpec.
type Thresholds struct {
	UnhealthyDuration    time.Duration
	JiraGracePeriod      time.Duration
	StaleReleaseDuration time.Duration
}

// Input is everything needed to evaluate one release group.
type Input struct {
	Name      string
	Namespace string
	Pods      []corev1.Pod
	// Prev is prior ReleaseState for this release (may be zero).
	Prev appsv1.ReleaseState
	// Now is the evaluation clock (injectable for tests).
	Now        time.Time
	Thresholds Thresholds
	Jira       JiraInfo
	Helm       HelmInfo
}

// Result is the evaluation outcome for status and deletion decisions.
type Result struct {
	Eligible     bool
	Reasons      []appsv1.DeletionReason
	Unhealthy    bool
	JiraIssue    string
	PodCount     int32
	LastDeployed *metav1.Time
	// State is the updated persistent state to store in status.releaseStates.
	State appsv1.ReleaseState
}

// Evaluate applies gate-independent OR rules for one Helm release group.
// Caller must only pass pods that already have the cleaner label and a release label.
func Evaluate(in Input) Result {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	out := Result{
		PodCount: int32(len(in.Pods)),
		State: appsv1.ReleaseState{
			Name:      in.Name,
			Namespace: in.Namespace,
		},
	}

	images := make([]string, 0, len(in.Pods)*2)
	unhealthyNow := false
	for i := range in.Pods {
		pod := &in.Pods[i]
		if podutil.IsUnhealthy(pod) {
			unhealthyNow = true
		}
		for _, c := range pod.Spec.Containers {
			images = append(images, c.Image)
		}
	}
	out.Unhealthy = unhealthyNow

	// Rule 1: Unhealthy duration via persistent unhealthySince.
	out.State.UnhealthySince = updateUnhealthySince(in.Prev.UnhealthySince, unhealthyNow, now)
	if unhealthyNow && out.State.UnhealthySince != nil {
		if now.Sub(out.State.UnhealthySince.Time) >= in.Thresholds.UnhealthyDuration {
			out.Reasons = append(out.Reasons, appsv1.DeletionReasonUnhealthy)
		}
	}

	// Rule 2: JIRA closed + grace.
	if issue, ok := ParseIssueKeyFromImages(images...); ok {
		out.JiraIssue = issue
		out.State.JiraIssue = issue
		if in.Jira.Found && in.Jira.Done && !in.Jira.ClosedAt.IsZero() {
			closedAt := metav1.NewTime(in.Jira.ClosedAt.UTC())
			out.State.JiraClosedAt = &closedAt
			if now.Sub(in.Jira.ClosedAt) >= in.Thresholds.JiraGracePeriod {
				out.Reasons = append(out.Reasons, appsv1.DeletionReasonJiraClosed)
			}
		}
	} else if in.Prev.JiraIssue != "" {
		// Keep last known issue in state only while pods still observed; clear closedAt if no longer parseable.
		out.State.JiraIssue = ""
		out.State.JiraClosedAt = nil
	}

	// Rule 3: Stale Helm release.
	if in.Helm.Found && !in.Helm.LastDeployed.IsZero() {
		ld := metav1.NewTime(in.Helm.LastDeployed.UTC())
		out.LastDeployed = &ld
		out.State.LastDeployed = &ld
		if now.Sub(in.Helm.LastDeployed) >= in.Thresholds.StaleReleaseDuration {
			out.Reasons = append(out.Reasons, appsv1.DeletionReasonStaleRelease)
		}
	}

	out.Eligible = len(out.Reasons) > 0
	out.State.LastReason = append([]appsv1.DeletionReason(nil), out.Reasons...)
	return out
}

func updateUnhealthySince(prev *metav1.Time, unhealthyNow bool, now time.Time) *metav1.Time {
	if !unhealthyNow {
		return nil
	}
	if prev != nil && !prev.IsZero() {
		return prev
	}
	t := metav1.NewTime(now)
	return &t
}
