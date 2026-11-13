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
	corev1 "k8s.io/api/core/v1"
)

// Waiting reasons that mark a Pod as unhealthy for Cleaner rule Unhealthy.
const (
	ReasonCrashLoopBackOff = "CrashLoopBackOff"
	ReasonImagePullBackOff = "ImagePullBackOff"
)

// IsUnhealthyWaitingReason reports whether reason is CrashLoopBackOff or ImagePullBackOff.
func IsUnhealthyWaitingReason(reason string) bool {
	return reason == ReasonCrashLoopBackOff || reason == ReasonImagePullBackOff
}

// IsUnhealthy returns true if any container (init or regular) is waiting with
// CrashLoopBackOff or ImagePullBackOff.
func IsUnhealthy(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	return UnhealthyReason(pod) != ""
}

// UnhealthyReason returns the first unhealthy waiting reason found on the Pod,
// or an empty string if the Pod is not unhealthy.
func UnhealthyReason(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	if reason := firstUnhealthyReason(pod.Status.InitContainerStatuses); reason != "" {
		return reason
	}
	return firstUnhealthyReason(pod.Status.ContainerStatuses)
}

func firstUnhealthyReason(statuses []corev1.ContainerStatus) string {
	for i := range statuses {
		waiting := statuses[i].State.Waiting
		if waiting == nil {
			continue
		}
		if IsUnhealthyWaitingReason(waiting.Reason) {
			return waiting.Reason
		}
	}
	return ""
}
