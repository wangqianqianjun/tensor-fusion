package constants

import "time"

const (
	// Domain is the domain prefix used for all tensor-fusion.ai related annotations and finalizers
	Domain = "tensor-fusion.ai"

	// Finalizer constants
	FinalizerSuffix = "finalizer"
	Finalizer       = Domain + "/" + FinalizerSuffix

	// Annotation key constants
	EnableContainerAnnotationFormat = Domain + "/enable-%s"
	TFLOPSContainerAnnotationFormat = Domain + "/tflops-%s"
	VRAMContainerAnnotationFormat   = Domain + "/vram-%s"

	PendingRequeueDuration = time.Second * 3

	GetConnectionURLEnv    = "TENSOR_FUSION_OPERATOR_GET_CONNECTION_URL"
	ConnectionNameEnv      = "TENSOR_FUSION_CONNECTION_NAME"
	ConnectionNamespaceEnv = "TENSOR_FUSION_CONNECTION_NAMESPACE"

	WorkerPortEnv = "TENSOR_FUSION_WORKER_PORT"
)
