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
	"testing"
	"time"

	appsv1 "altech.com/operator/api/v1"
	"altech.com/operator/internal/podutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func defaultThresholds() Thresholds {
	return Thresholds{
		UnhealthyDuration:    48 * time.Hour,
		JiraGracePeriod:      24 * time.Hour,
		StaleReleaseDuration: 336 * time.Hour,
	}
}

func unhealthyPod(image string) corev1.Pod {
	return corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: image}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app",
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: podutil.ReasonCrashLoopBackOff},
				},
			}},
		},
	}
}

func healthyPod(image string) corev1.Pod {
	return corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: image}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app",
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				},
			}},
		},
	}
}

func hasReason(reasons []appsv1.DeletionReason, want appsv1.DeletionReason) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

func TestEvaluateUnhealthy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("sets unhealthySince on first observation", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods:       []corev1.Pod{unhealthyPod("app:latest")},
			Now:        now,
			Thresholds: defaultThresholds(),
		})
		if !got.Unhealthy {
			t.Fatal("expected Unhealthy=true")
		}
		if got.Eligible {
			t.Fatal("expected not eligible after 0 duration")
		}
		if got.State.UnhealthySince == nil || !got.State.UnhealthySince.Time.Equal(now) {
			t.Fatalf("unhealthySince = %v, want %v", got.State.UnhealthySince, now)
		}
	})

	t.Run("not eligible before threshold", func(t *testing.T) {
		t.Parallel()
		since := metav1.NewTime(now.Add(-1 * time.Hour))
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{unhealthyPod("app:latest")},
			Prev: appsv1.ReleaseState{Name: "demo", Namespace: "ns", UnhealthySince: &since},
			Now:  now, Thresholds: defaultThresholds(),
		})
		if got.Eligible || hasReason(got.Reasons, appsv1.DeletionReasonUnhealthy) {
			t.Fatalf("unexpected eligible/reasons: %+v", got)
		}
		if got.State.UnhealthySince == nil || !got.State.UnhealthySince.Time.Equal(since.Time) {
			t.Fatal("should keep original unhealthySince")
		}
	})

	t.Run("eligible after 48h", func(t *testing.T) {
		t.Parallel()
		since := metav1.NewTime(now.Add(-49 * time.Hour))
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{unhealthyPod("app:latest")},
			Prev: appsv1.ReleaseState{Name: "demo", Namespace: "ns", UnhealthySince: &since},
			Now:  now, Thresholds: defaultThresholds(),
		})
		if !got.Eligible || !hasReason(got.Reasons, appsv1.DeletionReasonUnhealthy) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("resets unhealthySince when recovered", func(t *testing.T) {
		t.Parallel()
		since := metav1.NewTime(now.Add(-49 * time.Hour))
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:latest")},
			Prev: appsv1.ReleaseState{Name: "demo", Namespace: "ns", UnhealthySince: &since},
			Now:  now, Thresholds: defaultThresholds(),
		})
		if got.Unhealthy || got.State.UnhealthySince != nil || got.Eligible {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestEvaluateJiraClosed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("eligible after grace", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:PROJ-123-abc12")},
			Now:  now, Thresholds: defaultThresholds(),
			Jira: JiraInfo{Found: true, Done: true, ClosedAt: now.Add(-25 * time.Hour)},
		})
		if got.JiraIssue != "PROJ-123" {
			t.Fatalf("jiraIssue = %q", got.JiraIssue)
		}
		if !got.Eligible || !hasReason(got.Reasons, appsv1.DeletionReasonJiraClosed) {
			t.Fatalf("got %+v", got)
		}
		if got.State.JiraClosedAt == nil {
			t.Fatal("expected jiraClosedAt in state")
		}
	})

	t.Run("not eligible within grace", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:PROJ-123-abc12")},
			Now:  now, Thresholds: defaultThresholds(),
			Jira: JiraInfo{Found: true, Done: true, ClosedAt: now.Add(-1 * time.Hour)},
		})
		if got.Eligible || hasReason(got.Reasons, appsv1.DeletionReasonJiraClosed) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("not done", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:PROJ-123-abc12")},
			Now:  now, Thresholds: defaultThresholds(),
			Jira: JiraInfo{Found: true, Done: false, ClosedAt: now.Add(-48 * time.Hour)},
		})
		if got.Eligible {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("no matching tag", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:latest")},
			Now:  now, Thresholds: defaultThresholds(),
			Jira: JiraInfo{Found: true, Done: true, ClosedAt: now.Add(-48 * time.Hour)},
		})
		if got.JiraIssue != "" || got.Eligible {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestEvaluateStaleRelease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	t.Run("eligible after 14 days", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:latest")},
			Now:  now, Thresholds: defaultThresholds(),
			Helm: HelmInfo{Found: true, LastDeployed: now.Add(-15 * 24 * time.Hour)},
		})
		if !got.Eligible || !hasReason(got.Reasons, appsv1.DeletionReasonStaleRelease) {
			t.Fatalf("got %+v", got)
		}
		if got.LastDeployed == nil {
			t.Fatal("expected lastDeployed")
		}
	})

	t.Run("not stale yet", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:latest")},
			Now:  now, Thresholds: defaultThresholds(),
			Helm: HelmInfo{Found: true, LastDeployed: now.Add(-24 * time.Hour)},
		})
		if got.Eligible {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("helm not found skips rule", func(t *testing.T) {
		t.Parallel()
		got := Evaluate(Input{
			Name: "demo", Namespace: "ns",
			Pods: []corev1.Pod{healthyPod("app:latest")},
			Now:  now, Thresholds: defaultThresholds(),
			Helm: HelmInfo{Found: false},
		})
		if got.Eligible || hasReason(got.Reasons, appsv1.DeletionReasonStaleRelease) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestEvaluateMultipleReasons(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	since := metav1.NewTime(now.Add(-49 * time.Hour))
	got := Evaluate(Input{
		Name: "demo", Namespace: "ns",
		Pods: []corev1.Pod{unhealthyPod("app:PROJ-1-abc")},
		Prev: appsv1.ReleaseState{Name: "demo", Namespace: "ns", UnhealthySince: &since},
		Now:  now, Thresholds: defaultThresholds(),
		Jira: JiraInfo{Found: true, Done: true, ClosedAt: now.Add(-25 * time.Hour)},
		Helm: HelmInfo{Found: true, LastDeployed: now.Add(-20 * 24 * time.Hour)},
	})
	if !got.Eligible {
		t.Fatal("expected eligible")
	}
	for _, want := range []appsv1.DeletionReason{
		appsv1.DeletionReasonUnhealthy,
		appsv1.DeletionReasonJiraClosed,
		appsv1.DeletionReasonStaleRelease,
	} {
		if !hasReason(got.Reasons, want) {
			t.Fatalf("missing reason %s in %v", want, got.Reasons)
		}
	}
}
