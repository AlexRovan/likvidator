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

package podutil

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestIsUnhealthyWaitingReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason string
		want   bool
	}{
		{ReasonCrashLoopBackOff, true},
		{ReasonImagePullBackOff, true},
		{"ErrImagePull", false},
		{"Completed", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			t.Parallel()
			if got := IsUnhealthyWaitingReason(tt.reason); got != tt.want {
				t.Fatalf("IsUnhealthyWaitingReason(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestIsUnhealthy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "nil pod",
			pod:  nil,
			want: false,
		},
		{
			name: "running healthy",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "app",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					}},
				},
			},
			want: false,
		},
		{
			name: "crash loop",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "app",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: ReasonCrashLoopBackOff},
						},
					}},
				},
			},
			want: true,
		},
		{
			name: "image pull back off",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "app",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: ReasonImagePullBackOff},
						},
					}},
				},
			},
			want: true,
		},
		{
			name: "one of many unhealthy",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name: "sidecar",
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{},
							},
						},
						{
							Name: "app",
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{Reason: ReasonCrashLoopBackOff},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "init crash loop",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{{
						Name: "init",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: ReasonCrashLoopBackOff},
						},
					}},
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "app",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"},
						},
					}},
				},
			},
			want: true,
		},
		{
			name: "other waiting reason",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "app",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
						},
					}},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsUnhealthy(tt.pod); got != tt.want {
				t.Fatalf("IsUnhealthy() = %v, want %v", got, tt.want)
			}
			reason := UnhealthyReason(tt.pod)
			if tt.want && reason == "" {
				t.Fatal("UnhealthyReason() empty, want non-empty")
			}
			if !tt.want && reason != "" {
				t.Fatalf("UnhealthyReason() = %q, want empty", reason)
			}
		})
	}
}
