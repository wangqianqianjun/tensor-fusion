package config

import corev1 "k8s.io/api/core/v1"

type Config struct {
	PodMutator PodMutator `json:"podMutator"`
}

type PodMutator struct {
	PatchStrategicMerge corev1.Pod      `json:"patchStrategicMerge"`
	PatchEnvVars        []corev1.EnvVar `json:"envVars"`
}

func NewDefaultConfig() Config {
	return Config{}
}
