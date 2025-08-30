package cel_filter

import (
	"context"
	"fmt"
	"sync"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CELFilterConfig defines the configuration for CEL-based filtering
type CELFilterConfig struct {
	// CEL expression for filtering GPUs
	Expression string `json:"expression"`
	// Priority for this filter (higher priority filters run first)
	Priority int `json:"priority"`
	// Name for this filter (for debugging/logging)
	Name string `json:"name"`
}

// CELFilter implements GPU filtering using CEL expressions
type CELFilter struct {
	name       string
	expression string
	program    cel.Program
	env        *cel.Env
	mu         sync.RWMutex
}

// Filter applies the CEL expression to filter GPUs
func (f *CELFilter) Filter(ctx context.Context, workerPodKey tfv1.NameNamespace, gpus []*tfv1.GPU) ([]*tfv1.GPU, error) {
	log := log.FromContext(ctx)
	if len(gpus) == 0 {
		return gpus, nil
	}

	f.mu.RLock()
	program := f.program
	expression := f.expression
	f.mu.RUnlock()

	var filteredGPUs []*tfv1.GPU

	for _, gpu := range gpus {
		// Create variables for CEL evaluation
		vars := createCELVariables(*gpu, workerPodKey)

		// Evaluate the CEL expression
		result, _, err := program.Eval(vars)
		if err != nil {
			log.Error(err, "CEL expression evaluation failed",
				"expression", expression,
				"gpu", gpu.Name,
				"workerPodKey", workerPodKey)
			// On error, exclude the GPU (fail-safe)
			continue
		}

		// Convert result to boolean
		if boolResult, ok := result.(types.Bool); ok {
			if bool(boolResult) {
				filteredGPUs = append(filteredGPUs, gpu)
			}
		} else {
			log.Error(nil, "CEL expression did not return boolean",
				"expression", expression,
				"result", result,
				"gpu", gpu.Name)
			// On non-boolean result, exclude the GPU (fail-safe)
			continue
		}
	}

	log.V(1).Info("CEL filter applied",
		"filter", f.name,
		"expression", expression,
		"inputGPUs", len(gpus),
		"outputGPUs", len(filteredGPUs))

	return filteredGPUs, nil
}

// UpdateExpression updates the CEL expression (thread-safe)
func (f *CELFilter) UpdateExpression(newExpression string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	ast, issues := f.env.Compile(newExpression)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("failed to compile new CEL expression %q: %w", newExpression, issues.Err())
	}

	program, err := f.env.Program(ast)
	if err != nil {
		return fmt.Errorf("failed to create new CEL program: %w", err)
	}

	f.expression = newExpression
	f.program = program
	return nil
}

// createCELEnvironment creates a CEL environment with GPU-related variables and functions
func createCELEnvironment() (*cel.Env, error) {
	return cel.NewEnv(
		// Define GPU object structure
		cel.Variable(CELVarGPU, cel.MapType(cel.StringType, cel.DynType)),
		// Define worker pod key
		cel.Variable(CELVarWorkerPodKey, cel.MapType(cel.StringType, cel.StringType)),
		// Define request object structure
		cel.Variable(CELVarRequest, cel.MapType(cel.StringType, cel.DynType)),
	)
}

// createCELVariables creates variables for CEL evaluation from GPU and request information
func createCELVariables(gpu tfv1.GPU, workerPodKey tfv1.NameNamespace) map[string]interface{} {
	// Convert GPU to a map for CEL evaluation
	gpuMap := map[string]interface{}{
		GPUFieldName:        gpu.Name,
		GPUFieldNamespace:   gpu.Namespace,
		GPUFieldGPUModel:    gpu.Status.GPUModel,
		GPUFieldUUID:        gpu.Status.UUID,
		GPUFieldPhase:       string(gpu.Status.Phase),
		GPUFieldUsedBy:      string(gpu.Status.UsedBy),
		GPUFieldMessage:     gpu.Status.Message,
		GPUFieldLabels:      gpu.Labels,
		GPUFieldAnnotations: gpu.Annotations,
	}

	// Add capacity information if available
	if gpu.Status.Capacity != nil {
		gpuMap[GPUFieldCapacity] = map[string]interface{}{
			ResourceFieldTFlops: gpu.Status.Capacity.Tflops.AsApproximateFloat64(),
			ResourceFieldVRAM:   gpu.Status.Capacity.Vram.AsApproximateFloat64(),
		}
	}

	// Add available information if available
	if gpu.Status.Available != nil {
		gpuMap[GPUFieldAvailable] = map[string]interface{}{
			ResourceFieldTFlops: gpu.Status.Available.Tflops.AsApproximateFloat64(),
			ResourceFieldVRAM:   gpu.Status.Available.Vram.AsApproximateFloat64(),
		}
	}

	// Add node selector information
	if gpu.Status.NodeSelector != nil {
		gpuMap[GPUFieldNodeSelector] = gpu.Status.NodeSelector
	}

	// Add running apps information (always set, even if empty)
	runningApps := make([]map[string]interface{}, len(gpu.Status.RunningApps))
	for i, app := range gpu.Status.RunningApps {
		runningApps[i] = map[string]interface{}{
			AppFieldName:      app.Name,
			AppFieldNamespace: app.Namespace,
			AppFieldCount:     app.Count,
		}
	}
	gpuMap[GPUFieldRunningApps] = runningApps

	// Worker pod key information
	workerPodKeyMap := map[string]string{
		PodKeyFieldName:      workerPodKey.Name,
		PodKeyFieldNamespace: workerPodKey.Namespace,
	}

	return map[string]interface{}{
		CELVarGPU:          gpuMap,
		CELVarWorkerPodKey: workerPodKeyMap,
	}
}
