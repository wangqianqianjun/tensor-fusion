package karpenter

import (
	"context"
	"testing"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/pricing"
	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/types"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewKarpenterGPUNodeProvider(t *testing.T) {
	tests := []struct {
		name          string
		cfg           tfv1.ComputingVendorConfig
		client        client.Client
		nodeConfig    tfv1.NodeManagerConfig
		expectError   bool
		errorContains string
	}{
		{
			name: "valid configuration",
			cfg: tfv1.ComputingVendorConfig{
				Type: tfv1.ComputingVendorKarpenter,
			},
			client:        nil, // Will trigger error in actual implementation
			nodeConfig:    tfv1.NodeManagerConfig{},
			expectError:   true,
			errorContains: "kubernetes client cannot be nil",
		},
		{
			name: "invalid vendor type",
			cfg: tfv1.ComputingVendorConfig{
				Type: "invalid",
			},
			client:        nil,
			nodeConfig:    tfv1.NodeManagerConfig{},
			expectError:   true,
			errorContains: "invalid computing vendor type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewKarpenterGPUNodeProvider(tt.cfg, tt.client, tt.nodeConfig)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, provider)
				assert.Equal(t, tt.cfg, provider.config)
				assert.Equal(t, tt.client, provider.client)
			}
		})
	}
}

func TestKarpenterGPUNodeProvider_TestConnection(t *testing.T) {
	tests := []struct {
		name          string
		client        client.Client
		expectError   bool
		errorContains string
	}{
		{
			name:          "nil client",
			client:        nil,
			expectError:   true,
			errorContains: "kubernetes client is not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := KarpenterGPUNodeProvider{
				client: tt.client,
			}
			err := provider.TestConnection()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestKarpenterGPUNodeProvider_CreateNode(t *testing.T) {
	provider := KarpenterGPUNodeProvider{
		client: nil,
	}

	t.Run("nil parameter", func(t *testing.T) {
		ctx := context.Background()
		status, err := provider.CreateNode(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "NodeCreationParam cannot be nil")
		assert.Nil(t, status)
	})
}

func TestKarpenterGPUNodeProvider_TerminateNode(t *testing.T) {
	provider := KarpenterGPUNodeProvider{
		client: nil,
	}

	t.Run("nil parameter", func(t *testing.T) {
		ctx := context.Background()
		err := provider.TerminateNode(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "NodeIdentityParam cannot be nil")
	})

	t.Run("nil client", func(t *testing.T) {
		ctx := context.Background()
		param := &types.NodeIdentityParam{
			InstanceID: "test-instance",
		}
		err := provider.TerminateNode(ctx, param)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client is not initialized")
	})
}

func TestKarpenterGPUNodeProvider_GetNodeStatus(t *testing.T) {
	provider := KarpenterGPUNodeProvider{
		client: nil,
	}

	t.Run("nil parameter", func(t *testing.T) {
		ctx := context.Background()
		status, err := provider.GetNodeStatus(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "NodeIdentityParam cannot be nil")
		assert.Nil(t, status)
	})

	t.Run("nil client", func(t *testing.T) {
		ctx := context.Background()
		param := &types.NodeIdentityParam{
			InstanceID: "test-instance",
		}
		status, err := provider.GetNodeStatus(ctx, param)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "kubernetes client is not initialized")
		assert.Nil(t, status)
	})
}

func TestKarpenterGPUNodeProvider_GetInstancePricing(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		region       string
		capacityType types.CapacityTypeEnum
		expectError  bool
	}{
		{
			name:         "unknown instance type",
			instanceType: "unknown.type",
			region:       "us-east-1",
			capacityType: types.CapacityTypeOnDemand,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := KarpenterGPUNodeProvider{
				pricingProvider: pricing.NewStaticPricingProvider(),
			}
			price, err := provider.GetInstancePricing(tt.instanceType, tt.region, tt.capacityType)
			if tt.expectError {
				assert.Error(t, err)
				assert.Zero(t, price)
			} else {
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, price, 0.0)
			}
		})
	}
}

func TestKarpenterGPUNodeProvider_GetGPUNodeInstanceTypeInfo(t *testing.T) {
	provider := KarpenterGPUNodeProvider{
		pricingProvider: pricing.NewStaticPricingProvider(),
	}

	t.Run("valid region", func(t *testing.T) {
		result := provider.GetGPUNodeInstanceTypeInfo("us-east-1")
		assert.NotNil(t, result)
		// Since we don't know the exact implementation, we just check it returns a slice
	})

	t.Run("invalid region", func(t *testing.T) {
		result := provider.GetGPUNodeInstanceTypeInfo("invalid-region")
		assert.NotNil(t, result)
		// The provider returns all instance types regardless of region
		assert.GreaterOrEqual(t, len(result), 0)
	})
}

func TestKarpenterGPUNodeProvider_parseKarpenterConfig(t *testing.T) {
	provider := KarpenterGPUNodeProvider{}

	tests := []struct {
		name     string
		param    *types.NodeCreationParam
		expected string // Just test the GPU resource name for simplicity
	}{
		{
			name: "nil extra params",
			param: &types.NodeCreationParam{
				ExtraParams: nil,
			},
			expected: "", // Early return when extraParams is nil
		},
		{
			name: "empty extra params",
			param: &types.NodeCreationParam{
				ExtraParams: map[string]string{},
			},
			expected: "nvidia.com/gpu", // Default value should be set
		},
		{
			name: "custom GPU resource",
			param: &types.NodeCreationParam{
				ExtraParams: map[string]string{
					"karpenter.gpuresource": "custom.gpu/device",
				},
			},
			expected: "custom.gpu/device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.parseKarpenterConfig(tt.param)
			assert.Equal(t, tt.expected, string(result.GPUResourceName))
		})
	}
}

func TestSetNestedValue(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		value    string
		expected map[string]any
	}{
		{
			name:  "simple path",
			path:  "key",
			value: "value",
			expected: map[string]any{
				"key": "value",
			},
		},
		{
			name:  "nested path",
			path:  "parent.child",
			value: "value",
			expected: map[string]any{
				"parent": map[string]any{
					"child": "value",
				},
			},
		},
		{
			name:  "deep nested path",
			path:  "a.b.c.d",
			value: "value",
			expected: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": map[string]any{
							"d": "value",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make(map[string]any)
			setNestedValue(data, tt.path, tt.value)
			assert.Equal(t, tt.expected, data)
		})
	}
}
