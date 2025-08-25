# CEL Filters for GPU Allocation

CEL (Common Expression Language) filters provide a powerful and flexible way to define custom GPU filtering logic in TensorFusion. This feature allows you to write expressions that determine which GPUs are eligible for allocation based on various criteria.

## Overview

CEL filters are defined in the `SchedulingConfigTemplate` resource and are applied during the GPU allocation process. They work alongside traditional GPU filters and provide more sophisticated filtering capabilities.

## Configuration

CEL filters are configured in the `placement.celFilters` field of a `SchedulingConfigTemplate`:

```yaml
apiVersion: tensor-fusion.ai/v1
kind: SchedulingConfigTemplate
metadata:
  name: my-template
spec:
  placement:
    celFilters:
    - name: "filter-name"
      expression: "gpu.phase == 'Running'"
      priority: 100
```

### Fields

- `name` (optional): A descriptive name for the filter, used for logging and debugging
- `expression` (required): The CEL expression that returns a boolean value
- `priority` (optional, default: 0): Higher priority filters are applied first

## Available Variables

CEL expressions have access to the following variables:

### `gpu` Object

The `gpu` variable contains information about the GPU being evaluated:

```javascript
{
  "name": "gpu-1",           // GPU name
  "namespace": "default",     // GPU namespace
  "gpuModel": "NVIDIA A100",  // GPU model
  "uuid": "gpu-uuid",         // GPU UUID
  "phase": "Running",         // GPU phase (Running, Pending, etc.)
  "usedBy": "tensor-fusion",  // Usage system
  "labels": {...},           // Kubernetes labels
  "annotations": {...},      // Kubernetes annotations
  "capacity": {              // Total GPU capacity
    "tflops": 1.5,
    "vram": 85899345920      // in bytes
  },
  "available": {             // Available GPU resources
    "tflops": 1.0,
    "vram": 64424509440      // in bytes
  },
  "nodeSelector": {...},     // Node selector information
  "runningApps": [           // Currently running applications
    {
      "name": "app-1",
      "namespace": "default",
      "count": 1
    }
  ]
}
```

### `workerPodKey` Object

Information about the requesting worker pod:

```javascript
{
  "name": "worker-pod",
  "namespace": "default"
}
```

## Expression Examples

### Basic Filtering

```yaml
# Only use running GPUs
- name: "running-only"
  expression: "gpu.phase == 'Running'"
  priority: 100

# Filter by GPU model
- name: "nvidia-only"
  expression: "gpu.gpuModel.startsWith('NVIDIA')"
  priority: 90

# Ensure minimum resources available
- name: "min-resources"
  expression: "gpu.available.tflops >= 0.5 && gpu.available.vram >= 4294967296"
  priority: 80
```

### Label-Based Filtering

```yaml
# Filter by labels
- name: "premium-tier"
  expression: "gpu.labels != null && 'gpu-tier' in gpu.labels && gpu.labels['gpu-tier'] == 'premium'"
  priority: 70

# Multiple label conditions
- name: "training-gpus"
  expression: |
    gpu.labels != null && 
    'workload-type' in gpu.labels && 
    gpu.labels['workload-type'] == 'training' &&
    'zone' in gpu.labels && 
    gpu.labels['zone'].startsWith('us-west')
  priority: 60
```

### Resource-Based Filtering

```yaml
# Percentage of available resources
- name: "high-availability"
  expression: "gpu.available.tflops > gpu.capacity.tflops * 0.7"
  priority: 80

# Avoid overloaded GPUs
- name: "load-balancing"
  expression: "size(gpu.runningApps) < 3"
  priority: 50

# Memory-intensive workloads
- name: "high-memory"
  expression: "gpu.available.vram > 34359738368"  # > 32GB
  priority: 60
```

### Complex Conditions

```yaml
# Complex multi-criteria filter
- name: "complex-filter"
  expression: |
    gpu.phase == 'Running' && 
    gpu.gpuModel.contains('A100') &&
    gpu.available.tflops > 0.8 &&
    (
      size(gpu.runningApps) == 0 ||
      (size(gpu.runningApps) < 2 && gpu.available.vram > 42949672960)
    )
  priority: 90
```

## CEL Language Features

CEL supports many built-in functions and operators:

### String Operations
- `startsWith()`, `endsWith()`, `contains()`
- String concatenation with `+`
- Regular expressions with `matches()`

### Numeric Operations
- Standard arithmetic operators: `+`, `-`, `*`, `/`, `%`
- Comparison operators: `>`, `>=`, `<`, `<=`, `==`, `!=`

### Logical Operations
- `&&` (and), `||` (or), `!` (not)

### Collection Operations
- `size()` - get collection size
- `in` operator - check membership
- List/map access with `[]`

### Conditional Expressions
- Ternary operator: `condition ? true_value : false_value`

## Best Practices

### Performance
1. **Order by Priority**: Place most restrictive filters first (highest priority)
2. **Avoid Complex Expressions**: Keep expressions simple for better performance
3. **Cache-Friendly**: Use consistent filter logic to benefit from any caching

### Reliability
1. **Null Checks**: Always check for null values when accessing optional fields
2. **Fail-Safe Logic**: Design expressions to exclude GPUs on error rather than include them
3. **Test Thoroughly**: Test expressions with various GPU configurations

### Maintainability
1. **Descriptive Names**: Use clear, descriptive names for filters
2. **Comments**: Add comments for complex expressions
3. **Modular Design**: Break complex logic into multiple simpler filters

## Example Complete Configuration

```yaml
apiVersion: tensor-fusion.ai/v1
kind: SchedulingConfigTemplate
metadata:
  name: production-gpu-scheduling
spec:
  placement:
    mode: CompactFirst
    
    # Traditional filters (still supported)
    gpuFilters:
    - type: avoidTooMuchConnectionsOnSameGPU
      params:
        connectionNum: 100
    
    # CEL filters for advanced logic
    celFilters:
    # Critical filters (high priority)
    - name: "operational-gpus-only"
      expression: "gpu.phase == 'Running' && gpu.usedBy == 'tensor-fusion'"
      priority: 100
      
    - name: "sufficient-resources"
      expression: "gpu.available.tflops >= 0.3 && gpu.available.vram >= 2147483648"
      priority: 95
      
    # Preference filters (medium priority)
    - name: "prefer-nvidia"
      expression: "gpu.gpuModel.startsWith('NVIDIA')"
      priority: 80
      
    - name: "balanced-load"
      expression: "size(gpu.runningApps) < 2"
      priority: 70
      
    # Quality filters (lower priority)
    - name: "premium-hardware"
      expression: |
        gpu.labels != null && 
        'gpu-tier' in gpu.labels && 
        gpu.labels['gpu-tier'] in ['premium', 'high-performance']
      priority: 50
```

## Troubleshooting

### Common Issues

1. **Expression Compilation Errors**: Check syntax and ensure all referenced fields exist
2. **Runtime Errors**: Add null checks for optional fields
3. **No GPUs Selected**: Verify that at least some GPUs meet all filter criteria
4. **Performance Issues**: Simplify complex expressions or reduce the number of filters

### Debugging

Enable debug logging to see detailed information about filter execution:

```yaml
# In your logging configuration
logLevel: debug
```

Look for log entries containing "CEL filter applied" to see filtering results.

## Migration from Traditional Filters

CEL filters can be used alongside traditional GPU filters. They are applied after traditional filters in the filtering pipeline. You can gradually migrate complex traditional filters to CEL expressions for better maintainability.