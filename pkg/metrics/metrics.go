package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	errorCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:      "discoblocks_error_counter",
			Subsystem: "operator",
			Help:      "Counts all errors by type",
		},
		[]string{
			"resourceType", "resourceName", "resourceNamespace", "errorType", "operation",
		},
	)

	pvcOperationCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:      "discoblocks_pvc_operation_counter",
			Subsystem: "operator",
			Help:      "Counts all operations by type",
		},
		[]string{
			"resourceName", "resourceNamespace", "operation", "size",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(errorCounter)
	metrics.Registry.MustRegister(pvcOperationCounter)
}

// NewError increases error counter
func NewError(resourceType, resourceName, resourceNamespace, errorType, operation string) {
	errorCounter.WithLabelValues(resourceType, resourceName, resourceNamespace, errorType, operation).Inc()
}

// NewPVCOperation increases PVC operation counter
func NewPVCOperation(resourceName, resourceNamespace, operation, size string) {
	pvcOperationCounter.WithLabelValues(resourceName, resourceNamespace, operation, size).Inc()
}
