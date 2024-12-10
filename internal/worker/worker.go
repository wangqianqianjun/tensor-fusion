package worker

import (
	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type WorkerGenerator struct {
	PodTemplate *corev1.PodTemplate
}

func (wg *WorkerGenerator) GenerateConnectionURL(_gpu *tfv1.GPU, _connection *tfv1.TensorFusionConnection) string {
	return "TODO://"
}

func (wg *WorkerGenerator) GenerateWorkerPod(
	connection *tfv1.TensorFusionConnection,
	namespacedName types.NamespacedName,
) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespacedName.Name,
			Namespace: namespacedName.Namespace,
		},
		Spec: wg.PodTemplate.Template.Spec,
	}
}
