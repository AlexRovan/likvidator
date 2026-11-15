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

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	appsv1 "altech.com/operator/api/v1"
	"altech.com/operator/internal/helm"
	"altech.com/operator/internal/jira"
	"altech.com/operator/internal/policy"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	reasonSecretError   = "SecretError"
	reasonJiraError     = "JiraError"
	reasonScanError     = "ScanError"
	reasonReconcileOK   = "ReconcileSucceeded"
	reasonDryRunDelete  = "DryRunWouldDelete"
	reasonUninstalled   = "Uninstalled"
	reasonUninstallFail = "UninstallFailed"
)

// CleanerReconciler reconciles a Cleaner object.
type CleanerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// Helm overrides the Helm client (tests). If nil, built from Client.
	Helm helm.Interface
	// HTTPClient is used for JIRA (tests). If nil, http.DefaultClient.
	HTTPClient *http.Client
	// Now overrides the clock (tests).
	Now func() time.Time
}

// +kubebuilder:rbac:groups=apps.altech.com,resources=cleaners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.altech.com,resources=cleaners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.altech.com,resources=cleaners/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;delete
// +kubebuilder:rbac:groups="",resources=services;serviceaccounts;persistentvolumeclaims,verbs=get;list;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets;replicasets,verbs=get;list;delete
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses;networkpolicies,verbs=get;list;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;delete

// Reconcile scans labeled pods and uninstalls eligible Helm releases.
func (r *CleanerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cleaner appsv1.Cleaner
	if err := r.Get(ctx, req.NamespacedName, &cleaner); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get Cleaner: %w", err)
	}

	now := r.now()
	interval := durationOr(cleaner.Spec.Interval, mustParseDuration(appsv1.DefaultInterval))

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cleaner.Namespace,
		Name:      cleaner.Spec.Jira.SecretRef.Name,
	}, secret); err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonSecretError, fmt.Sprintf("Could not get JIRA Secret: %v", err))
		return ctrl.Result{}, fmt.Errorf("get JIRA Secret: %w", err)
	}

	creds, err := jira.CredentialsFromSecret(secret)
	if err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonSecretError, err.Error())
		return ctrl.Result{}, fmt.Errorf("parse JIRA Secret: %w", err)
	}

	jiraClient, err := jira.NewClient(creds, r.HTTPClient)
	if err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonJiraError, err.Error())
		return ctrl.Result{}, fmt.Errorf("create JIRA client: %w", err)
	}

	helmClient, err := r.helmClient()
	if err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonScanError, err.Error())
		return ctrl.Result{}, fmt.Errorf("create Helm client: %w", err)
	}

	namespaces, err := r.resolveNamespaces(ctx, &cleaner)
	if err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonScanError, err.Error())
		return ctrl.Result{}, fmt.Errorf("resolve namespaces: %w", err)
	}

	cleanerLabelKey := stringOr(cleaner.Spec.CleanerLabelKey, appsv1.DefaultCleanerLabelKey)
	releaseLabelKey := stringOr(cleaner.Spec.ReleaseLabelKey, appsv1.DefaultReleaseLabelKey)

	pods, err := r.listCleanerPods(ctx, namespaces, cleanerLabelKey)
	if err != nil {
		_ = r.patchDegraded(ctx, &cleaner, reasonScanError, err.Error())
		return ctrl.Result{}, fmt.Errorf("list pods: %w", err)
	}

	groups := groupPodsByRelease(pods, releaseLabelKey, log)

	thresholds := policy.Thresholds{
		UnhealthyDuration:    durationOr(cleaner.Spec.UnhealthyDuration, mustParseDuration(appsv1.DefaultUnhealthyDuration)),
		JiraGracePeriod:      durationOr(cleaner.Spec.JiraGracePeriod, mustParseDuration(appsv1.DefaultJiraGracePeriod)),
		StaleReleaseDuration: durationOr(cleaner.Spec.StaleReleaseDuration, mustParseDuration(appsv1.DefaultStaleReleaseDuration)),
	}

	prevStates := indexReleaseStates(cleaner.Status.ReleaseStates)
	jiraCache := map[string]policy.JiraInfo{}

	observed := make([]appsv1.ObservedRelease, 0, len(groups))
	candidates := make([]appsv1.CandidateRelease, 0)
	deleted := make([]appsv1.DeletedRelease, 0)
	newStates := make([]appsv1.ReleaseState, 0, len(groups))

	keys := make([]releaseKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].namespace == keys[j].namespace {
			return keys[i].name < keys[j].name
		}
		return keys[i].namespace < keys[j].namespace
	})

	var uninstallErr error
	for _, key := range keys {
		podList := groups[key]
		helmInfo := policy.HelmInfo{}
		rel, herr := helmClient.Get(ctx, key.name, key.namespace)
		switch {
		case herr == nil:
			helmInfo = policy.HelmInfo{Found: true, LastDeployed: rel.LastDeployed}
		case errors.Is(herr, helm.ErrNotFound):
			log.Info("Helm release not found in storage", "release", key.name, "namespace", key.namespace)
		default:
			log.Error(herr, "Could not get Helm release", "release", key.name, "namespace", key.namespace)
		}

		jiraInfo := policy.JiraInfo{}
		if issueKey, ok := policy.ParseIssueKeyFromImages(podImages(podList)...); ok {
			if cached, hit := jiraCache[issueKey]; hit {
				jiraInfo = cached
			} else {
				issue, jerr := jiraClient.GetIssue(ctx, issueKey)
				switch {
				case jerr == nil:
					jiraInfo = issue.ToPolicy()
				case errors.Is(jerr, jira.ErrNotFound):
					jiraInfo = policy.JiraInfo{Found: false}
				default:
					log.Error(jerr, "Could not get JIRA issue", "issue", issueKey)
					jiraInfo = policy.JiraInfo{Found: false}
				}
				jiraCache[issueKey] = jiraInfo
			}
		}

		result := policy.Evaluate(policy.Input{
			Name:       key.name,
			Namespace:  key.namespace,
			Pods:       podList,
			Prev:       prevStates[key],
			Now:        now,
			Thresholds: thresholds,
			Jira:       jiraInfo,
			Helm:       helmInfo,
		})
		newStates = append(newStates, result.State)

		obs := appsv1.ObservedRelease{
			Name:         key.name,
			Namespace:    key.namespace,
			PodCount:     result.PodCount,
			LastDeployed: result.LastDeployed,
			JiraIssue:    result.JiraIssue,
			Eligible:     result.Eligible,
			Reasons:      result.Reasons,
			Unhealthy:    result.Unhealthy,
		}
		observed = append(observed, obs)

		if !result.Eligible {
			continue
		}

		candidates = append(candidates, appsv1.CandidateRelease{
			Name:      key.name,
			Namespace: key.namespace,
			Reasons:   result.Reasons,
			DryRun:    cleaner.Spec.DryRun,
		})

		if cleaner.Spec.DryRun {
			r.event(&cleaner, corev1.EventTypeWarning, reasonDryRunDelete,
				fmt.Sprintf("Would uninstall Helm release %s/%s reasons=%v", key.namespace, key.name, result.Reasons))
			deleted = append(deleted, appsv1.DeletedRelease{
				Name:      key.name,
				Namespace: key.namespace,
				Reasons:   result.Reasons,
				DryRun:    true,
				DeletedAt: &metav1.Time{Time: now},
			})
			continue
		}

		if err := helmClient.Uninstall(ctx, key.name, key.namespace); err != nil {
			if errors.Is(err, helm.ErrNotFound) {
				log.Info("Helm release already gone", "release", key.name, "namespace", key.namespace)
			} else {
				r.event(&cleaner, corev1.EventTypeWarning, reasonUninstallFail,
					fmt.Sprintf("Could not uninstall Helm release %s/%s: %v", key.namespace, key.name, err))
				uninstallErr = err
				continue
			}
		}
		r.event(&cleaner, corev1.EventTypeNormal, reasonUninstalled,
			fmt.Sprintf("Uninstalled Helm release %s/%s reasons=%v", key.namespace, key.name, result.Reasons))
		deleted = append(deleted, appsv1.DeletedRelease{
			Name:      key.name,
			Namespace: key.namespace,
			Reasons:   result.Reasons,
			DryRun:    false,
			DeletedAt: &metav1.Time{Time: now},
		})
	}

	if err := r.patchStatus(ctx, &cleaner, func(status *appsv1.CleanerStatus) {
		status.LastScanTime = &metav1.Time{Time: now}
		status.SelectedNamespaces = namespaces
		status.ObservedReleases = observed
		status.Candidates = candidates
		status.Deleted = deleted
		status.ReleaseStates = newStates
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             reasonReconcileOK,
			Message:            "Scan completed",
			ObservedGeneration: cleaner.Generation,
		})
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionDegraded,
			Status:             metav1.ConditionFalse,
			Reason:             reasonReconcileOK,
			Message:            "Scan completed",
			ObservedGeneration: cleaner.Generation,
		})
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionProgressing,
			Status:             metav1.ConditionFalse,
			Reason:             reasonReconcileOK,
			Message:            "Scan completed",
			ObservedGeneration: cleaner.Generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch Cleaner status: %w", err)
	}

	if uninstallErr != nil {
		return ctrl.Result{}, fmt.Errorf("uninstall Helm release: %w", uninstallErr)
	}

	log.Info("Completed Cleaner scan",
		"namespaces", len(namespaces),
		"observed", len(observed),
		"candidates", len(candidates),
		"deleted", len(deleted),
	)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CleanerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("cleaner-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Cleaner{}).
		Named("cleaner").
		Complete(r)
}

func (r *CleanerReconciler) helmClient() (helm.Interface, error) {
	if r.Helm != nil {
		return r.Helm, nil
	}
	return helm.NewClient(r.Client)
}

func (r *CleanerReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *CleanerReconciler) event(cleaner *appsv1.Cleaner, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(cleaner, eventType, reason, message)
}

func (r *CleanerReconciler) resolveNamespaces(ctx context.Context, cleaner *appsv1.Cleaner) ([]string, error) {
	if isEmptyNamespaceSelector(cleaner.Spec.NamespaceSelector) {
		return []string{cleaner.Namespace}, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(cleaner.Spec.NamespaceSelector)
	if err != nil {
		return nil, fmt.Errorf("invalid namespaceSelector: %w", err)
	}
	var nsList corev1.NamespaceList
	if err := r.List(ctx, &nsList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nsList.Items))
	for i := range nsList.Items {
		names = append(names, nsList.Items[i].Name)
	}
	sort.Strings(names)
	return names, nil
}

func (r *CleanerReconciler) listCleanerPods(ctx context.Context, namespaces []string, cleanerLabelKey string) ([]corev1.Pod, error) {
	req, err := labels.NewRequirement(cleanerLabelKey, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("build pod label requirement: %w", err)
	}
	selector := labels.NewSelector().Add(*req)

	out := make([]corev1.Pod, 0)
	for _, ns := range namespaces {
		var list corev1.PodList
		if err := r.List(ctx, &list, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: selector}); err != nil {
			return nil, fmt.Errorf("list pods in %q: %w", ns, err)
		}
		for i := range list.Items {
			pod := list.Items[i]
			if pod.Labels[cleanerLabelKey] == "" {
				continue
			}
			out = append(out, pod)
		}
	}
	return out, nil
}

func (r *CleanerReconciler) patchDegraded(ctx context.Context, cleaner *appsv1.Cleaner, reason, message string) error {
	return r.patchStatus(ctx, cleaner, func(status *appsv1.CleanerStatus) {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: cleaner.Generation,
		})
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               appsv1.ConditionDegraded,
			Status:             metav1.ConditionTrue,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: cleaner.Generation,
		})
	})
}

func (r *CleanerReconciler) patchStatus(ctx context.Context, cleaner *appsv1.Cleaner, mutate func(*appsv1.CleanerStatus)) error {
	latest := &appsv1.Cleaner{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cleaner), latest); err != nil {
		return err
	}
	patch := client.MergeFrom(latest.DeepCopy())
	mutate(&latest.Status)
	return r.Status().Patch(ctx, latest, patch)
}

type releaseKey struct {
	namespace string
	name      string
}

func groupPodsByRelease(pods []corev1.Pod, releaseLabelKey string, log logr.Logger) map[releaseKey][]corev1.Pod {
	groups := map[releaseKey][]corev1.Pod{}
	for i := range pods {
		pod := pods[i]
		releaseName := pod.Labels[releaseLabelKey]
		if releaseName == "" {
			log.Info("Skipped Pod without release label", "pod", pod.Name, "namespace", pod.Namespace, "label", releaseLabelKey)
			continue
		}
		key := releaseKey{namespace: pod.Namespace, name: releaseName}
		groups[key] = append(groups[key], pod)
	}
	return groups
}

func indexReleaseStates(states []appsv1.ReleaseState) map[releaseKey]appsv1.ReleaseState {
	out := make(map[releaseKey]appsv1.ReleaseState, len(states))
	for i := range states {
		s := states[i]
		out[releaseKey{namespace: s.Namespace, name: s.Name}] = s
	}
	return out
}

func podImages(pods []corev1.Pod) []string {
	images := make([]string, 0, len(pods))
	for i := range pods {
		for _, c := range pods[i].Spec.Containers {
			images = append(images, c.Image)
		}
	}
	return images
}

func isEmptyNamespaceSelector(sel *metav1.LabelSelector) bool {
	return sel == nil || (len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0)
}

func stringOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func durationOr(d *metav1.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return d.Duration
}

func mustParseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}
