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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types for Cleaner status.
const (
	ConditionReady       = "Ready"
	ConditionDegraded    = "Degraded"
	ConditionProgressing = "Progressing"
)

// DeletionReason is an OR-rule that made a release eligible for uninstall.
// +kubebuilder:validation:Enum=Unhealthy;JiraClosed;StaleRelease
type DeletionReason string

const (
	DeletionReasonUnhealthy    DeletionReason = "Unhealthy"
	DeletionReasonJiraClosed   DeletionReason = "JiraClosed"
	DeletionReasonStaleRelease DeletionReason = "StaleRelease"
)

// Default values for CleanerSpec fields (applied by CRD OpenAPI defaults / controller).
const (
	DefaultCleanerLabelKey      = "cleaner"
	DefaultReleaseLabelKey      = "release"
	DefaultInterval             = "15m"
	DefaultUnhealthyDuration    = "48h"
	DefaultJiraGracePeriod      = "24h"
	DefaultStaleReleaseDuration = "336h"
)

// CleanerSpec defines the desired state of Cleaner.
type CleanerSpec struct {
	// Interval is the requeue period after a successful scan.
	// +kubebuilder:default="15m"
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// DryRun logs and updates status without calling helm uninstall.
	// +kubebuilder:default=false
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// NamespaceSelector selects Namespaces to scan by their labels.
	// If absent or empty, only the Cleaner's own namespace is scanned.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// CleanerLabelKey is the Pod label gate; missing/empty value skips the Pod.
	// +kubebuilder:default="cleaner"
	// +optional
	CleanerLabelKey string `json:"cleanerLabelKey,omitempty"`

	// ReleaseLabelKey is the Pod label that holds the Helm release name.
	// +kubebuilder:default="release"
	// +optional
	ReleaseLabelKey string `json:"releaseLabelKey,omitempty"`

	// UnhealthyDuration is how long CrashLoopBackOff/ImagePullBackOff must persist
	// before rule Unhealthy applies.
	// +kubebuilder:default="48h"
	// +optional
	UnhealthyDuration *metav1.Duration `json:"unhealthyDuration,omitempty"`

	// JiraGracePeriod is how long after a JIRA issue is closed before rule JiraClosed applies.
	// +kubebuilder:default="24h"
	// +optional
	JiraGracePeriod *metav1.Duration `json:"jiraGracePeriod,omitempty"`

	// StaleReleaseDuration is how long since Helm last_deployed before rule StaleRelease applies.
	// +kubebuilder:default="336h"
	// +optional
	StaleReleaseDuration *metav1.Duration `json:"staleReleaseDuration,omitempty"`

	// Jira configures access to JIRA for rule JiraClosed.
	// +required
	Jira JiraSpec `json:"jira"`
}

// JiraSpec holds JIRA connection settings.
type JiraSpec struct {
	// SecretRef is the name of a Secret in the Cleaner's namespace with keys:
	// url, apiToken, and optional username.
	// +required
	SecretRef SecretReference `json:"secretRef"`
}

// SecretReference names a Secret in the same namespace as the Cleaner.
type SecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
}

// CleanerStatus defines the observed state of Cleaner.
type CleanerStatus struct {
	// Conditions represent the current state of the Cleaner resource.
	// Types: Ready, Degraded, Progressing. Use metav1.Condition with ObservedGeneration.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastScanTime is when the last full scan completed.
	// +optional
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`

	// SelectedNamespaces are namespaces matched by namespaceSelector on the last scan.
	// +optional
	SelectedNamespaces []string `json:"selectedNamespaces,omitempty"`

	// ObservedReleases are all Helm releases seen on the last scan
	// (groups of pods with cleaner + release labels), not only deletion candidates.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	ObservedReleases []ObservedRelease `json:"observedReleases,omitempty"`

	// Candidates are observed releases currently eligible for deletion.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	Candidates []CandidateRelease `json:"candidates,omitempty"`

	// Deleted are releases uninstalled (or that would be uninstalled in dry-run)
	// on the last / recent pass.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	Deleted []DeletedRelease `json:"deleted,omitempty"`

	// ReleaseStates holds persistent per-release timer/grace state across reconciles.
	// +listType=map
	// +listMapKey=name
	// +listMapKey=namespace
	// +optional
	ReleaseStates []ReleaseState `json:"releaseStates,omitempty"`
}

// ObservedRelease is one Helm release group seen during a scan.
type ObservedRelease struct {
	// Name is the Helm release name from the release label.
	// +required
	Name string `json:"name"`

	// Namespace is the release namespace (pod namespace).
	// +required
	Namespace string `json:"namespace"`

	// PodCount is the number of observed pods for this release on the scan.
	// +optional
	PodCount int32 `json:"podCount,omitempty"`

	// LastDeployed is info.last_deployed from Helm when available.
	// +optional
	LastDeployed *metav1.Time `json:"lastDeployed,omitempty"`

	// JiraIssue is the parsed issue key from an image tag, if any.
	// +optional
	JiraIssue string `json:"jiraIssue,omitempty"`

	// Eligible is true when the release is a deletion candidate.
	// +optional
	Eligible bool `json:"eligible,omitempty"`

	// Reasons are the OR-rules that fired; empty when not eligible.
	// +optional
	Reasons []DeletionReason `json:"reasons,omitempty"`

	// Unhealthy is true if any observed pod is in CrashLoopBackOff or ImagePullBackOff.
	// +optional
	Unhealthy bool `json:"unhealthy,omitempty"`
}

// CandidateRelease is a release eligible for uninstall on the last scan.
type CandidateRelease struct {
	// Name is the Helm release name.
	// +required
	Name string `json:"name"`

	// Namespace is the release namespace.
	// +required
	Namespace string `json:"namespace"`

	// Reasons are the OR-rules that made the release eligible.
	// +optional
	Reasons []DeletionReason `json:"reasons,omitempty"`

	// DryRun mirrors spec.dryRun at evaluation time.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
}

// DeletedRelease records an uninstall (or dry-run would-delete) outcome.
type DeletedRelease struct {
	// Name is the Helm release name.
	// +required
	Name string `json:"name"`

	// Namespace is the release namespace.
	// +required
	Namespace string `json:"namespace"`

	// Reasons are the OR-rules that triggered deletion.
	// +optional
	Reasons []DeletionReason `json:"reasons,omitempty"`

	// DryRun is true when only a would-delete was recorded.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// DeletedAt is when the uninstall or dry-run decision was recorded.
	// +optional
	DeletedAt *metav1.Time `json:"deletedAt,omitempty"`
}

// ReleaseState is persistent per-release state used for unhealthy/JIRA/stale timers.
type ReleaseState struct {
	// Name is the Helm release name.
	// +required
	Name string `json:"name"`

	// Namespace is the release namespace.
	// +required
	Namespace string `json:"namespace"`

	// UnhealthySince is when a bad waiting reason was first observed continuously.
	// +optional
	UnhealthySince *metav1.Time `json:"unhealthySince,omitempty"`

	// JiraIssue is the last known issue key parsed from an image tag.
	// +optional
	JiraIssue string `json:"jiraIssue,omitempty"`

	// JiraClosedAt is when the JIRA issue entered a done category.
	// +optional
	JiraClosedAt *metav1.Time `json:"jiraClosedAt,omitempty"`

	// LastDeployed mirrors Helm info.last_deployed for observability.
	// +optional
	LastDeployed *metav1.Time `json:"lastDeployed,omitempty"`

	// LastReason is the last evaluated set of deletion reasons for this release.
	// +optional
	LastReason []DeletionReason `json:"lastReason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="DryRun",type=boolean,JSONPath=".spec.dryRun"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Cleaner is the Schema for the cleaners API.
// It periodically scans Pods and uninstalls Helm releases that match deletion rules.
type Cleaner struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Cleaner
	// +required
	Spec CleanerSpec `json:"spec"`

	// status defines the observed state of Cleaner
	// +optional
	Status CleanerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CleanerList contains a list of Cleaner
type CleanerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Cleaner `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Cleaner{}, &CleanerList{})
		return nil
	})
}
