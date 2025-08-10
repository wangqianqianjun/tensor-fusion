/*
Copyright 2024.

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
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/config"
	"github.com/NexusGPU/tensor-fusion/internal/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ = Describe("TensorFusionPodMutator", func() {
	var (
		mutator *TensorFusionPodMutator
		ctx     context.Context
		scheme  *runtime.Scheme
		decoder admission.Decoder
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(tfv1.AddToScheme(scheme)).To(Succeed())

		decoder = admission.NewDecoder(scheme)

		mutator = &TensorFusionPodMutator{
			Client:  k8sClient,
			decoder: decoder,
		}
	})

	Context("Handle", func() {
		It("should handle pod with empty namespace", func() {
			// Create a pod with empty namespace
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pod-empty-ns",
					// empty namespace
					Namespace: "",
					Labels: map[string]string{
						constants.TensorFusionEnabledLabelKey: "true",
					},
					Annotations: map[string]string{
						constants.GpuPoolKey:                "mock",
						constants.InjectContainerAnnotation: "main",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "test-workload",
							UID:        "owner-uid",
							Controller: ptr.To(true),
						},
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "test-image",
					}},
				},
			}
			podBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())

			// Construct an admission request with the pod
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: podBytes,
					},
					Operation: admissionv1.Create,
					Namespace: "default",
				},
			}

			// Call mutator.Handle to process the admission request
			resp := mutator.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
		})

		It("should successfully mutate a pod with TF resources", func() {
			// Set up a workload profile for testing
			workloadProfile := &tfv1.WorkloadProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-profile-handle",
					Namespace: "default",
				},
				Spec: tfv1.WorkloadProfileSpec{
					PoolName: "mock",
					Resources: tfv1.Resources{
						Requests: tfv1.Resource{
							Tflops: resource.MustParse("10"),
							Vram:   resource.MustParse("1Gi"),
						},
						Limits: tfv1.Resource{
							Tflops: resource.MustParse("100"),
							Vram:   resource.MustParse("16Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workloadProfile)).To(Succeed())

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Labels: map[string]string{
						constants.TensorFusionEnabledLabelKey: "true",
					},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-workload",
						UID:        "owner-uid",
						Controller: ptr.To(true),
					}},
					Annotations: map[string]string{
						constants.GpuPoolKey:                "mock",
						constants.WorkloadProfileAnnotation: "test-profile-handle",
						constants.InjectContainerAnnotation: "main",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "test-image",
							Env: []corev1.EnvVar{
								{
									Name: "A",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
								{
									Name:  "B",
									Value: "B",
								},
							},
						},
					},
				},
			}
			podBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: podBytes,
					},
					Operation: admissionv1.Create,
				},
			}

			resp := mutator.Handle(ctx, req)
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			// Check workload created
			Eventually(func(g Gomega) error {
				workload := &tfv1.TensorFusionWorkload{}
				err = k8sClient.Get(ctx, client.ObjectKey{Name: "test-workload", Namespace: "default"}, workload)
				g.Expect(workload.Spec.Replicas).To(BeNil())
				g.Expect(workload.OwnerReferences).To(HaveLen(1))
				g.Expect(workload.OwnerReferences[0].Name).To(Equal("test-workload"))
				return err
			}).Should(Succeed())
		})

		It("should handle pods without TF requirements", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-no-tf",
					Namespace: "default",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "test-image",
						},
					},
				},
			}

			podBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: podBytes,
					},
					Operation: admissionv1.Create,
				},
			}

			resp := mutator.Handle(ctx, req)
			// Should fail because no annotations are found
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).To(BeEmpty())
		})

		It("should handle invalid pod specification", func() {
			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: []byte("invalid json"),
					},
					Operation: admissionv1.Create,
				},
			}

			resp := mutator.Handle(ctx, req)
			Expect(resp.Allowed).To(BeFalse())
			Expect(resp.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})
	})

	Context("Handle with local GPU mode", func() {
		It("should successfully handle a pod with local GPU mode", func() {
			// Set up a workload profile with IsLocalGPU set to true
			workloadProfile := &tfv1.WorkloadProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local-gpu-profile",
					Namespace: "default",
				},
				Spec: tfv1.WorkloadProfileSpec{
					PoolName:   "mock",
					IsLocalGPU: true,
					Resources: tfv1.Resources{
						Requests: tfv1.Resource{
							Tflops: resource.MustParse("10"),
							Vram:   resource.MustParse("1Gi"),
						},
						Limits: tfv1.Resource{
							Tflops: resource.MustParse("100"),
							Vram:   resource.MustParse("16Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workloadProfile)).To(Succeed())

			// Create a TensorFusionWorkload first
			workload := &tfv1.TensorFusionWorkload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local-gpu-workload",
					Namespace: "default",
				},
				Spec: tfv1.WorkloadProfileSpec{
					PoolName:   "mock",
					IsLocalGPU: true,
				},
			}
			Expect(k8sClient.Create(ctx, workload)).To(Succeed())

			Expect(k8sClient.Status().Update(ctx, workload)).To(Succeed())

			// Create a pod with the local GPU profile
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod-local-gpu",
					Namespace: "default",
					Labels: map[string]string{
						constants.TensorFusionEnabledLabelKey: "true",
					},
					Annotations: map[string]string{
						constants.GpuPoolKey:                "mock",
						constants.WorkloadProfileAnnotation: "local-gpu-profile",
						constants.InjectContainerAnnotation: "main",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "test-image",
						},
					},
				},
			}
			podBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: podBytes,
					},
					Operation: admissionv1.Create,
				},
			}

			// Process the request
			resp := mutator.Handle(ctx, req)

			// Verify the response
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			scheduleMutation, found := lo.Find(resp.Patches, func(patch jsonpatch.JsonPatchOperation) bool {
				return patch.Path == "/spec/schedulerName"
			})
			Expect(found).To(BeTrue())
			Expect(scheduleMutation.Value).To(Equal(constants.SchedulerName))

			workloadAnnotationMutation, found := lo.Find(resp.Patches, func(patch jsonpatch.JsonPatchOperation) bool {
				return patch.Path == "/metadata/annotations/tensor-fusion.ai~1tflops-limit"
			})
			Expect(found).To(BeTrue())
			Expect(workloadAnnotationMutation.Value).To(Equal("100"))
		})
	})

	Context("Handle with EnabledReplicas", func() {
		It("should only patch enabledReplicas pods", func() {
			// Create a ReplicaSet as the owner for the pod
			replicaSet := &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rs",
					Namespace: "default",
				},
				Spec: appsv1.ReplicaSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "test-app",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "test-app",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "test-container",
									Image: "test-image",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, replicaSet)).To(Succeed())

			// Get the ReplicaSet to obtain its UID
			createdReplicaSet := &appsv1.ReplicaSet{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: "test-rs"}, createdReplicaSet)).To(Succeed())
			replicaSetUID := createdReplicaSet.GetUID()

			// Create a workload profile
			workloadProfile := &tfv1.WorkloadProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-profile-enabled-replicas",
					Namespace: "default",
				},
				Spec: tfv1.WorkloadProfileSpec{
					PoolName: "mock",
					Resources: tfv1.Resources{
						Requests: tfv1.Resource{
							Tflops: resource.MustParse("10"),
							Vram:   resource.MustParse("1Gi"),
						},
						Limits: tfv1.Resource{
							Tflops: resource.MustParse("100"),
							Vram:   resource.MustParse("16Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, workloadProfile)).To(Succeed())

			// Create a pod with TF resources and owner reference
			trueVal := true
			enabledReplicas := int32(1)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:    "default",
					GenerateName: "test-pod-enabled-replicas-",
					Labels: map[string]string{
						constants.TensorFusionEnabledLabelKey: "true",
						"pod-template-hash":                   "test-hash",
					},
					Annotations: map[string]string{
						constants.GpuPoolKey:                            "mock",
						constants.WorkloadProfileAnnotation:             "test-profile-enabled-replicas",
						constants.InjectContainerAnnotation:             "main",
						constants.TensorFusionEnabledReplicasAnnotation: fmt.Sprintf("%d", enabledReplicas), // Using the correct constant
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "ReplicaSet",
							Name:       "test-rs",
							UID:        replicaSetUID,
							Controller: &trueVal,
						},
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "test-image",
						},
					},
				},
			}

			podBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())

			req := admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw: podBytes,
					},
					Operation: admissionv1.Create,
				},
			}

			resp := mutator.Handle(ctx, req)
			// First call: Pod mutation should occur since enabledReplicas is 1,
			// so the response should be allowed and contain patches
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).NotTo(BeEmpty())

			counter := &TensorFusionPodCounter{Client: k8sClient}
			count, _, err := counter.Get(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(int32(1)))

			resp = mutator.Handle(ctx, req)
			// Second call: Pod should be ignored since it's been processed already,
			// so the response should be allowed but patches should be empty
			Expect(resp.Allowed).To(BeTrue())
			Expect(resp.Patches).To(BeEmpty())

			// Clean up
			Expect(k8sClient.Delete(ctx, replicaSet)).To(Succeed())
			Expect(k8sClient.Delete(ctx, workloadProfile)).To(Succeed())
		})
	})

	Context("ParseTensorFusionInfo", func() {
		It("should correctly parse TF requirements from pod annotations", func() {
			// Set up a workload profile for testing
			workloadProfile := &tfv1.WorkloadProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-profile-parse-tf-resources",
					Namespace: "default",
				},
				Spec: tfv1.WorkloadProfileSpec{
					PoolName: "mock",
					Resources: tfv1.Resources{
						Requests: tfv1.Resource{
							Tflops: resource.MustParse("10"),
							Vram:   resource.MustParse("1Gi"),
						},
						Limits: tfv1.Resource{
							Tflops: resource.MustParse("100"),
							Vram:   resource.MustParse("16Gi"),
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, workloadProfile)).To(Succeed())

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Annotations: map[string]string{
						constants.GpuPoolKey:                "mock",
						constants.WorkloadProfileAnnotation: "test-profile-parse-tf-resources",
						// override tflops request
						constants.TFLOPSRequestAnnotation:               "20",
						constants.InjectContainerAnnotation:             "test-container",
						constants.TensorFusionEnabledReplicasAnnotation: "3",
						constants.QoSLevelAnnotation:                    "high",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
						},
					},
				},
			}
			tfInfo, err := ParseTensorFusionInfo(ctx, k8sClient, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(tfInfo.ContainerNames).To(HaveLen(1))
			Expect(tfInfo.ContainerNames[0]).To(Equal("test-container"))
			Expect(tfInfo.Profile.PoolName).To(Equal("mock"))
			Expect(tfInfo.Profile.Resources.Requests.Tflops.String()).To(Equal("20"))
			Expect(tfInfo.Profile.Resources.Requests.Vram.String()).To(Equal("1Gi"))
			Expect(tfInfo.Profile.Resources.Limits.Tflops.String()).To(Equal("100"))
			Expect(tfInfo.Profile.Resources.Limits.Vram.String()).To(Equal("16Gi"))
			Expect(tfInfo.Profile.Qos).To(Equal(tfv1.QoSHigh))
			Expect(*tfInfo.EnabledReplicas).To(Equal(int32(3)))
		})
	})

	Context("patchTFClient", func() {
		It("should apply the patch to the pod", func() {
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "test-container",
						},
					},
				},
			}

			pool := &tfv1.GPUPool{
				Spec: *config.MockGPUPoolSpec,
			}

			currentBytes, err := json.Marshal(pod)
			Expect(err).NotTo(HaveOccurred())
			patch, err := mutator.patchTFClient(pod, pool, false, currentBytes, []int{0})
			Expect(err).NotTo(HaveOccurred())
			Expect(patch).NotTo(BeEmpty())
			// There should be at least 2 patches (initContainers and the container env patches)
			Expect(len(patch)).To(BeNumerically(">=", 2))
		})
	})
})
