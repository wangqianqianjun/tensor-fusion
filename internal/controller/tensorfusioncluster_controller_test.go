/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TensorFusionCluster Controller", func() {
	Context("When reconciling a cluster", func() {
		It("Should create a specific number of GPUPools according to spec", func() {
			By("checking one pool in spec")
			tfEnv := NewTensorFusionEnvBuilder().AddPoolWithNodeCount(0).Build()
			Expect(tfEnv.GetGPUPoolList().Items).Should(HaveLen(1))

			By("checking two pools in spec")
			tfEnv = NewTensorFusionEnvBuilder().
				AddPoolWithNodeCount(0).
				AddPoolWithNodeCount(0).
				Build()
			Expect(tfEnv.GetGPUPoolList().Items).Should(HaveLen(2))

			By("checking that the pool should be recreated if it is manually deleted")
			pool := tfEnv.GetGPUPool(0)
			Expect(k8sClient.Delete(ctx, pool)).Should(Succeed())
			Expect(tfEnv.GetGPUPoolList().Items).Should(HaveLen(2))

			tfEnv.Cleanup()
		})

		It("Can manage multiple pools", func() {
			tfEnv := NewTensorFusionEnvBuilder().
				AddPoolWithNodeCount(2).SetGpuCountPerNode(2).
				AddPoolWithNodeCount(1).SetGpuCountPerNode(2).
				Build()
			tfEnv.Cleanup()
		})
	})
})
