package config

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

type Config struct {
	WorkerTemplate corev1.PodTemplate `json:"workerTemplate"`
	PodMutator     PodMutator         `json:"podMutator"`
}

type PodMutator struct {
	PatchStrategicMerge corev1.Pod      `json:"patchStrategicMerge"`
	PatchEnvVars        []corev1.EnvVar `json:"envVars"`
}

func NewDefaultConfig() Config {
	return Config{
		WorkerTemplate: corev1.PodTemplate{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr.To[int64](0),
					Containers: []corev1.Container{
						{
							Name:    "tensorfusion-worker",
							Image:   "busybox:stable-glibc",
							Command: []string{"sleep", "infinity"},
						},
					},
				},
			},
		},
	}
}
