package constants

const (
	// TensorFusionDomain is the domain prefix used for all tensor-fusion.ai related annotations and finalizers
	TensorFusionDomain = "tensor-fusion.ai"

	// Finalizer constants
	TensorFusionFinalizerSuffix = "finalizer"
	TensorFusionFinalizer       = TensorFusionDomain + "/" + TensorFusionFinalizerSuffix

	// Annotation key constants
	EnableContainerAnnotationFormat = TensorFusionDomain + "/enable-%s"
	TFLOPSContainerAnnotationFormat = TensorFusionDomain + "/tflops-%s"
	VRAMContainerAnnotationFormat   = TensorFusionDomain + "/vram-%s"
)
