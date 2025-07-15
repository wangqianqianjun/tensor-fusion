package pricing

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/aws-gpu.csv
var awsCSV string

//go:embed testdata/azure-gpu.csv
var azureCSV string

func InitializePricingProvider(t *testing.T) {
	loadCSVInstanceDataFromPath([]byte(awsCSV), providerAWS)
	loadCSVInstanceDataFromPath([]byte(azureCSV), providerAzure)
}

func TestStaticPricingProvider_AWS(t *testing.T) {
	InitializePricingProvider(t)
	provider := NewStaticPricingProvider()

	// Test AWS instance pricing - use an instance type with available pricing
	instanceType := "p2.16xlarge"
	region := "us-east-1"

	// Test on-demand pricing
	price, found := provider.GetPringcing(instanceType, types.CapacityTypeOnDemand)
	if found {
		assert.Greater(t, price, 0.0, "AWS on-demand price should be greater than 0")
		t.Logf("AWS %s on-demand price: $%.4f/hour", instanceType, price)
	}

	// Test getting pricing with capacity type
	onDemandPrice, foundOnDemand := provider.GetPringcing(instanceType, types.CapacityTypeOnDemand)
	if foundOnDemand {
		assert.Greater(t, onDemandPrice, 0.0, "AWS on-demand price should be greater than 0")
		t.Logf("AWS %s on-demand price (via GetPringcing): $%.4f/hour", instanceType, onDemandPrice)
	}

	reservedPrice, foundReserved := provider.GetPringcing(instanceType, types.CapacityTypeReserved)
	if foundReserved {
		assert.GreaterOrEqual(t, reservedPrice, 0.0, "AWS reserved price should be >= 0")
		t.Logf("AWS %s reserved price: $%.4f/hour", instanceType, reservedPrice)
	}

	// Test getting GPU instance info by instance type
	infos, foundInfo := provider.GetGPUNodeInstanceTypeInfoByInstance(instanceType, region)
	if foundInfo && len(infos) > 0 {
		info := infos[0]
		assert.Equal(t, instanceType, info.InstanceType, "Instance type should match")
		assert.Greater(t, info.GPUCount, int32(0), "GPU count should be greater than 0")
		assert.NotEmpty(t, info.GPUModel, "GPU model should not be empty")

		t.Logf("AWS %s GPU info:", instanceType)
		t.Logf("  GPU Model: %s", info.GPUModel)
		t.Logf("  GPU Count: %d", info.GPUCount)
		t.Logf("  FP16 TFlops per GPU: %d", info.FP16TFlopsPerGPU)
		t.Logf("  VRAM per GPU: %d GB", info.VRAMGigabytesPerGPU)
		t.Logf("  Memory: %d GiB", info.MemoryGiB)
		t.Logf("  CPU Architecture: %s", info.CPUArchitecture)
	}

	// Test getting all instance types
	instanceTypes, foundTypes := provider.GetGPUNodeInstanceTypeInfo(region)
	if foundTypes {
		assert.Greater(t, len(instanceTypes), 0, "Should have some instance types")
		t.Logf("Found %d total instance types in region %s", len(instanceTypes), region)

		// Log first few instance types
		for i, instanceType := range instanceTypes {
			if i < 5 {
				t.Logf("  Instance type %d: %s", i+1, instanceType.InstanceType)
			}
		}
	}
}

func TestStaticPricingProvider_Azure(t *testing.T) {
	InitializePricingProvider(t)
	provider := NewStaticPricingProvider()

	// Test Azure instance pricing
	instanceType := "ND12s"
	region := "eastus"

	// Test on-demand pricing
	price, found := provider.GetPringcing(instanceType, types.CapacityTypeOnDemand)
	if found {
		assert.Greater(t, price, 0.0, "Azure on-demand price should be greater than 0")
		t.Logf("Azure %s on-demand price: $%.4f/hour", instanceType, price)
	}

	// Test getting pricing with capacity type
	onDemandPrice, foundOnDemand := provider.GetPringcing(instanceType, types.CapacityTypeOnDemand)
	if foundOnDemand {
		assert.Greater(t, onDemandPrice, 0.0, "Azure on-demand price should be greater than 0")
		t.Logf("Azure %s on-demand price (via GetPringcing): $%.4f/hour", instanceType, onDemandPrice)
	}

	reservedPrice, foundReserved := provider.GetPringcing(instanceType, types.CapacityTypeReserved)
	if foundReserved {
		assert.GreaterOrEqual(t, reservedPrice, 0.0, "Azure reserved price should be >= 0")
		t.Logf("Azure %s reserved price: $%.4f/hour", instanceType, reservedPrice)
	}

	spotPrice, foundSpot := provider.GetPringcing(instanceType, types.CapacityTypeSpot)
	if foundSpot {
		assert.GreaterOrEqual(t, spotPrice, 0.0, "Azure spot price should be >= 0")
		t.Logf("Azure %s spot price: $%.4f/hour", instanceType, spotPrice)
	}

	// Test getting GPU instance info by instance type
	infos, foundInfo := provider.GetGPUNodeInstanceTypeInfoByInstance(instanceType, region)
	if foundInfo && len(infos) > 0 {
		info := infos[0]
		assert.Equal(t, instanceType, info.InstanceType, "Instance type should match")
		assert.Greater(t, info.GPUCount, int32(0), "GPU count should be greater than 0")
		assert.NotEmpty(t, info.GPUModel, "GPU model should not be empty")

		t.Logf("Azure %s GPU info:", instanceType)
		t.Logf("  GPU Model: %s", info.GPUModel)
		t.Logf("  GPU Count: %d", info.GPUCount)
		t.Logf("  FP16 TFlops per GPU: %d", info.FP16TFlopsPerGPU)
		t.Logf("  VRAM per GPU: %d GB", info.VRAMGigabytesPerGPU)
		t.Logf("  Memory: %d GiB", info.MemoryGiB)
		t.Logf("  CPU Architecture: %s", info.CPUArchitecture)
	}
}

func TestParseHelperFunctions(t *testing.T) {
	InitializePricingProvider(t)
	// Test parsePrice
	assert.Equal(t, 21.5, parsePrice("$21.5000 hourly"))
	assert.Equal(t, 0.0, parsePrice("unavailable"))
	assert.Equal(t, 98.32, parsePrice("$98.3200 hourly"))

	// Test parseGPUCount
	assert.Equal(t, int32(8), parseGPUCount("8"))
	assert.Equal(t, int32(16), parseGPUCount("16"))
	assert.Equal(t, int32(0), parseGPUCount("invalid"))

	// Test parseMemory
	assert.Equal(t, int32(512), parseMemory("512 GiB"))
	assert.Equal(t, int32(2048), parseMemory("2048 GiB"))
	assert.Equal(t, int32(0), parseMemory("invalid"))

	// Test parseAzureGPUSpec
	count, model := parseAzureGPUSpec("8X V100 (NVlink)")
	assert.Equal(t, int32(8), count)
	assert.Equal(t, "V100", model)

	count, model = parseAzureGPUSpec("1X H100")
	assert.Equal(t, int32(1), count)
	assert.Equal(t, "H100", model)

	count, model = parseAzureGPUSpec("4X A100")
	assert.Equal(t, int32(4), count)
	assert.Equal(t, "A100", model)
}

func TestIsFractionalGPUCount(t *testing.T) {
	InitializePricingProvider(t)
	provider := NewStaticPricingProvider()
	price, found := provider.GetPringcing("NV12ads v710 v5", types.CapacityTypeOnDemand)
	assert.False(t, found)
	assert.Equal(t, 0.0, price)
}

func TestUnavaliableGPUCount(t *testing.T) {
	InitializePricingProvider(t)
	provider := NewStaticPricingProvider()
	price, found := provider.GetPringcing("NG32ads V620 v1", types.CapacityTypeOnDemand)
	assert.False(t, found)
	assert.Equal(t, 0.0, price)
}

func TestAZGPUNodeInstanceInfo(t *testing.T) {
	InitializePricingProvider(t)
	provider := NewStaticPricingProvider()
	ND12s, found := provider.GetGPUNodeInstanceTypeInfoByInstance("ND12s", "eastus")
	assert.True(t, found)
	assert.Equal(t, int32(2), ND12s[0].GPUCount)
	assert.Equal(t, "P40", ND12s[0].GPUModel)
	assert.Equal(t, int32(24), ND12s[0].VRAMGigabytesPerGPU)

	ND96isr, found := provider.GetGPUNodeInstanceTypeInfoByInstance("ND96isr H200 v5", "eastus")
	assert.True(t, found)
	assert.Equal(t, int32(8), ND96isr[0].GPUCount)
	assert.Equal(t, "H200", ND96isr[0].GPUModel)
	assert.Equal(t, int32(141), ND96isr[0].VRAMGigabytesPerGPU)
}

func writeAWSCSV(t *testing.T, path string, price float64) {
	t.Helper()
	content := fmt.Sprintf(
		`Name,API Name,Instance Memory,GPUs,GPU model,GPU memory,Arch,Network Performance,On Demand,Linux Reserved cost,Linux Spot Minimum cost,Windows On Demand cost,Windows Reserved cost
TRN1 32xlarge,trn1.32xlarge,512 GiB,16,AWS Inferentia,512 GiB,x86_64,8x 100 Gigabit,$%.4f hourly,$13.2289 hourly,unavailable,unavailable,unavailable
`, price)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func writeAzureCSV(t *testing.T, path string, price float64) {
	t.Helper()
	content := fmt.Sprintf(
		`Name,API Name,Instance Memory,GPUs,Linux On Demand cost,Linux Reserved cost,Linux Spot cost,Windows On Demand cost,Windows Savings Plan,Windows Reserved cost,Windows Spot cost,GPU memory(per gpu)
Standard ND40rs v2,ND40rs v2,672 GiB,8X V100 (NVlink),$%.4f hourly,$10.7957 hourly,$3.9658 hourly,$23.8720 hourly,$20.1794 hourly,$12.6357 hourly,$4.2970 hourly,32GB
`, price)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

const (
	awsInstanceType   = "trn1.32xlarge"
	azureInstanceType = "ND40rs v2"

	awsPriceInit   = 21.50
	awsPriceUpdate = 99.99

	azurePriceInit   = 22.0320
	azurePriceUpdate = 88.88
)

func TestFileWatcherReloadsPricingData(t *testing.T) {
	tmp := t.TempDir()
	awsFile := filepath.Join(tmp, "aws.csv")
	azureFile := filepath.Join(tmp, "azure.csv")

	writeAWSCSV(t, awsFile, awsPriceInit)
	writeAzureCSV(t, azureFile, azurePriceInit)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	InitializePricingData(awsFile, azureFile, ctx)
	provider := NewStaticPricingProvider()

	require.Eventually(t, func() bool {
		_, ok1 := provider.GetPringcing(awsInstanceType, types.CapacityTypeOnDemand)
		_, ok2 := provider.GetPringcing(azureInstanceType, types.CapacityTypeOnDemand)
		return ok1 && ok2
	}, 3*time.Second, 20*time.Millisecond, "initial pricing data not loaded")

	price, ok := provider.GetPringcing(awsInstanceType, types.CapacityTypeOnDemand)
	require.True(t, ok)
	require.Equal(t, awsPriceInit, price)

	price, ok = provider.GetPringcing(azureInstanceType, types.CapacityTypeOnDemand)
	require.True(t, ok)
	require.Equal(t, azurePriceInit, price)

	writeAWSCSV(t, awsFile, awsPriceUpdate)
	writeAzureCSV(t, azureFile, azurePriceUpdate)

	require.Eventually(t, func() bool {
		p1, ok1 := provider.GetPringcing(awsInstanceType, types.CapacityTypeOnDemand)
		return ok1 && p1 == awsPriceUpdate
	}, 30*time.Second, 10*time.Second, "pricing data not updated")
}
