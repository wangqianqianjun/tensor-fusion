package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	GpuTflopsRequest = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_tflops_request",
		},
		[]string{"namespace", "pod", "container"},
	)

	GpuTflopsLimit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gpu_tflops_limit",
		},
		[]string{"namespace", "pod", "container"},
	)

	VramBytesRequest = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vram_bytes_request",
		},
		[]string{"namespace", "pod", "container"},
	)

	VramBytesLimit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vram_bytes_limit",
		},
		[]string{"namespace", "pod", "container"},
	)
)

func init() {
	metrics.Registry.MustRegister(GpuTflopsRequest, GpuTflopsLimit, VramBytesRequest, VramBytesLimit)
}
