package metrics

import "github.com/prometheus/client_golang/prometheus"

var registry = prometheus.NewRegistry()

var (
	MM1MOTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "mm1_mo_total",
			Help:      "Total MM1 mobile originated messages.",
		},
		[]string{"status"},
	)
	MM1MTTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "mm1_mt_total",
			Help:      "Total MM1 mobile terminated messages.",
		},
		[]string{"status"},
	)
	MM4InboundTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "mm4_inbound_total",
			Help:      "Total inbound MM4 messages.",
		},
		[]string{"peer", "status"},
	)
	MM4OutboundTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "mm4_outbound_total",
			Help:      "Total outbound MM4 messages.",
		},
		[]string{"peer", "status"},
	)
	MM7SubmitTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "mm7_submit_total",
			Help:      "Total MM7 submits.",
		},
		[]string{"vasp", "status"},
	)
	WAPPushTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "wap_push_total",
			Help:      "Total WAP Push notifications.",
		},
		[]string{"status"},
	)
	SMPPSubmitTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "smpp_submit_total",
			Help:      "Total SMPP submits.",
		},
		[]string{"status"},
	)
	MessageSizeBytes = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "message_size_bytes",
			Help:      "Observed MMS message size.",
			Buckets:   prometheus.ExponentialBuckets(1024, 2, 10),
		},
	)
	StoreOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "store_operation_duration_seconds",
			Help:      "Storage operation latency.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"op", "backend"},
	)
	AdaptDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "adapt_duration_seconds",
			Help:      "Adaptation stage latency.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"type"},
	)
	QueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "vectorcore",
			Subsystem: "mmsc",
			Name:      "queue_depth",
			Help:      "Messages pending delivery processing.",
		},
	)
)

func init() {
	registry.MustRegister(
		MM1MOTotal,
		MM1MTTotal,
		MM4InboundTotal,
		MM4OutboundTotal,
		MM7SubmitTotal,
		WAPPushTotal,
		SMPPSubmitTotal,
		MessageSizeBytes,
		StoreOpDuration,
		AdaptDuration,
		QueueDepth,
	)
}

func Registry() *prometheus.Registry {
	return registry
}
