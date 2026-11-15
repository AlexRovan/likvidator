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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "altech.com/operator/api/v1"
	"altech.com/operator/internal/helm"
	"altech.com/operator/internal/podutil"
)

// Envtest scenarios (SPEC §10):
// - empty scan with no cleaner-labeled pods
// - stale Helm release + dryRun → candidates/deleted, no uninstall
// - pod without cleaner label → ignored
// - unhealthy >= 48h + dryRun=false → mock Helm uninstall
var _ = Describe("Cleaner Controller", func() {
	const (
		resourceName      = "test-resource"
		resourceNamespace = "default"
		jiraSecretName    = "jira-credentials"
	)

	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	BeforeEach(func() {
		By("creating JIRA credentials Secret")
		secret := &corev1.Secret{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: jiraSecretName, Namespace: resourceNamespace}, secret)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jiraSecretName,
					Namespace: resourceNamespace,
				},
				Data: map[string][]byte{
					"url":      []byte("https://jira.example.com"),
					"apiToken": []byte("token"),
				},
			})).To(Succeed())
		}

		By("creating the Cleaner resource")
		cleaner := &appsv1.Cleaner{}
		err = k8sClient.Get(ctx, typeNamespacedName, cleaner)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, &appsv1.Cleaner{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: resourceNamespace,
				},
				Spec: appsv1.CleanerSpec{
					DryRun: true,
					Jira: appsv1.JiraSpec{
						SecretRef: appsv1.SecretReference{Name: jiraSecretName},
					},
				},
			})).To(Succeed())
		}
	})

	AfterEach(func() {
		resource := &appsv1.Cleaner{}
		err := k8sClient.Get(ctx, typeNamespacedName, resource)
		if err == nil {
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		}

		secret := &corev1.Secret{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: jiraSecretName, Namespace: resourceNamespace}, secret)
		if err == nil {
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		}

		pod := &corev1.Pod{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "demo-pod", Namespace: resourceNamespace}, pod)
		if err == nil {
			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
		}
	})

	It("should reconcile with empty scan when no labeled pods", func() {
		fakeHelm := &helm.Fake{}
		reconciler := &CleanerReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(10),
			Helm:     fakeHelm,
			Now:      func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) },
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Cleaner{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
		Expect(updated.Status.SelectedNamespaces).To(Equal([]string{resourceNamespace}))
		Expect(updated.Status.ObservedReleases).To(BeEmpty())
		Expect(updated.Status.Candidates).To(BeEmpty())
	})

	It("should observe release and dry-run delete when stale", func() {
		now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		fakeHelm := &helm.Fake{}
		fakeHelm.SetRelease(helm.ReleaseInfo{
			Name:         "demo-app",
			Namespace:    resourceNamespace,
			LastDeployed: now.Add(-20 * 24 * time.Hour),
		})

		Expect(k8sClient.Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo-pod",
				Namespace: resourceNamespace,
				Labels: map[string]string{
					"cleaner": "true",
					"release": "demo-app",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "app",
					Image: "registry.example.com/demo:latest",
				}},
			},
		})).To(Succeed())

		reconciler := &CleanerReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(10),
			Helm:     fakeHelm,
			Now:      func() time.Time { return now },
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Cleaner{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
		Expect(updated.Status.ObservedReleases).To(HaveLen(1))
		Expect(updated.Status.ObservedReleases[0].Name).To(Equal("demo-app"))
		Expect(updated.Status.ObservedReleases[0].Eligible).To(BeTrue())
		Expect(updated.Status.Candidates).To(HaveLen(1))
		Expect(updated.Status.Deleted).To(HaveLen(1))
		Expect(updated.Status.Deleted[0].DryRun).To(BeTrue())
		Expect(fakeHelm.Uninstalled).To(BeEmpty())
	})

	It("should skip pods without cleaner label", func() {
		Expect(k8sClient.Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo-pod",
				Namespace: resourceNamespace,
				Labels: map[string]string{
					"release": "demo-app",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "app",
					Image: "registry.example.com/demo:latest",
				}},
			},
		})).To(Succeed())

		reconciler := &CleanerReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(10),
			Helm:     &helm.Fake{},
			Now:      func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) },
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		updated := &appsv1.Cleaner{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
		Expect(updated.Status.ObservedReleases).To(BeEmpty())
	})

	It("should uninstall when dryRun is false and unhealthy long enough", func() {
		now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		fakeHelm := &helm.Fake{}
		fakeHelm.SetRelease(helm.ReleaseInfo{
			Name:         "crash-app",
			Namespace:    resourceNamespace,
			LastDeployed: now.Add(-1 * time.Hour),
		})

		cleaner := &appsv1.Cleaner{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, cleaner)).To(Succeed())
		cleaner.Spec.DryRun = false
		Expect(k8sClient.Update(ctx, cleaner)).To(Succeed())

		// Seed status with unhealthySince so rule 1 fires on first reconcile.
		cleaner.Status.ReleaseStates = []appsv1.ReleaseState{{
			Name:           "crash-app",
			Namespace:      resourceNamespace,
			UnhealthySince: &metav1.Time{Time: now.Add(-49 * time.Hour)},
		}}
		Expect(k8sClient.Status().Update(ctx, cleaner)).To(Succeed())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo-pod",
				Namespace: resourceNamespace,
				Labels: map[string]string{
					"cleaner": "true",
					"release": "crash-app",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:  "app",
					Image: "registry.example.com/demo:latest",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: "app",
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: podutil.ReasonCrashLoopBackOff},
			},
		}}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		reconciler := &CleanerReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(10),
			Helm:     fakeHelm,
			Now:      func() time.Time { return now },
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(fakeHelm.Uninstalled).To(Equal([]string{resourceNamespace + "/crash-app"}))
	})
})
