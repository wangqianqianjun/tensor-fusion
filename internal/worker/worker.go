package worker

import (
	"fmt"
	"strconv"
	"time"

	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
	"github.com/NexusGPU/tensor-fusion-operator/internal/config"
	"github.com/NexusGPU/tensor-fusion-operator/internal/constants"
	"github.com/samber/lo"
	"golang.org/x/exp/rand"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func init() {
	rand.Seed(uint64(time.Now().UnixNano()))
}

type WorkerGenerator struct {
	WorkerConfig *config.Worker
}

func (wg *WorkerGenerator) GenerateConnectionURL(connection *tfv1.TensorFusionConnection, pod *corev1.Pod) (string, error) {
	port, ok := lo.Find(pod.Spec.Containers[0].Env, func(env corev1.EnvVar) bool {
		return env.Name == constants.WorkerPortEnv
	})

	if !ok {
		return "", fmt.Errorf("worker port not found in pod %s", pod.Name)
	}
	return fmt.Sprintf("native+%s+%d", pod.Status.PodIP, port.Value), nil
}

func (wg *WorkerGenerator) AllocPort() int16 {
	min := 30000
	max := 65535
	return int16(rand.Intn(max-min+1) + min)
}

func (wg *WorkerGenerator) GenerateWorkerPod(
	gpu *tfv1.GPU,
	connection *tfv1.TensorFusionConnection,
	namespacedName types.NamespacedName,
	port int16,
) *corev1.Pod {
	spec := wg.WorkerConfig.Template.Spec.DeepCopy()
	if spec.NodeSelector == nil {
		spec.NodeSelector = make(map[string]string)
	}
	spec.NodeSelector = gpu.Status.NodeSelector

	spec.Containers[0].Env = append(spec.Containers[0].Env, corev1.EnvVar{
		Name:  "NVIDIA_VISIBLE_DEVICES",
		Value: gpu.Status.UUID,
	}, corev1.EnvVar{
		Name:  constants.WorkerPortEnv,
		Value: strconv.Itoa(int(port)),
	})

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespacedName.Name,
			Namespace: namespacedName.Namespace,
		},
		Spec: *spec,
	}
}
