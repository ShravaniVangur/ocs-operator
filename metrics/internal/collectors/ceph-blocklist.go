package collectors

import (
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog/v2"
)

var _ prometheus.Collector = &CephBlocklistCollector{}

type CephBlocklistCollector struct {
	rbdCollector    *CephRBDCollector
	NodeRBDBlocklist *prometheus.Desc
}

func NewCephBlocklistCollector(rbdCollector *CephRBDCollector) *CephBlocklistCollector {
	return &CephBlocklistCollector{
		rbdCollector: rbdCollector,
		NodeRBDBlocklist: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "rbd_client_blocklisted"),
			"State of the rbd client on a node, 0 = Unblocked, 1 = Blocked",
			[]string{"node", "consumer_name"}, nil),
	}
}

func (c *CephBlocklistCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.NodeRBDBlocklist
}

func (c *CephBlocklistCollector) Collect(ch chan<- prometheus.Metric) {
	blockedNodes := c.rbdCollector.BlockedNodes()
	if blockedNodes == nil {
		klog.V(4).Info("blocklist cache not yet populated, skipping")
		return
	}
	for node, consumerName := range blockedNodes {
		ch <- prometheus.MustNewConstMetric(c.NodeRBDBlocklist,
			prometheus.GaugeValue, 1,
			node, consumerName,
		)
	}
}
