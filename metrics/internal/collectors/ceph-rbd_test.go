package collectors

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestChunkImages(t *testing.T) {
	tests := []struct {
		name       string
		images     []string
		wantChunks int
		wantLast   int
	}{
		{
			name:       "empty",
			images:     nil,
			wantChunks: 0,
		},
		{
			name:       "under chunk size",
			images:     makeImageNames(50),
			wantChunks: 1,
			wantLast:   50,
		},
		{
			name:       "exact chunk size",
			images:     makeImageNames(imageChunkSize),
			wantChunks: 1,
			wantLast:   imageChunkSize,
		},
		{
			name:       "one over",
			images:     makeImageNames(imageChunkSize + 1),
			wantChunks: 2,
			wantLast:   1,
		},
		{
			name:       "three full chunks",
			images:     makeImageNames(imageChunkSize * 3),
			wantChunks: 3,
			wantLast:   imageChunkSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkImages("pool", "ns", tt.images)
			if len(chunks) != tt.wantChunks {
				t.Fatalf("got %d chunks, want %d", len(chunks), tt.wantChunks)
			}
			if tt.wantChunks > 0 {
				last := chunks[len(chunks)-1]
				if len(last.images) != tt.wantLast {
					t.Errorf("last chunk has %d images, want %d", len(last.images), tt.wantLast)
				}
				if last.pool != "pool" || last.radosNamespace != "ns" {
					t.Errorf("chunk pool/ns = %s/%s, want pool/ns", last.pool, last.radosNamespace)
				}
			}
		})
	}
}

func TestCephRBDCollectorCollect(t *testing.T) {
	c := &CephRBDCollector{
		pvMetadata: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "rbd", "pv_metadata"),
			"test", []string{"name", "image", "pool_name", "rados_namespace", "consumer_name"}, nil,
		),
		childrenCount: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "rbd", "children_count"),
			"test", []string{"image", "pool_name", "rados_namespace", "consumer_name"}, nil,
		),
		mirrorState: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "rbd_mirror", "image_state"),
			"test", []string{"image", "pool_name", "site_name", "consumer_name"}, nil,
		),
	}

	t.Run("nil cache returns no metrics", func(t *testing.T) {
		ch := make(chan prometheus.Metric, 10)
		c.Collect(ch)
		close(ch)
		if len(ch) != 0 {
			t.Errorf("expected 0 metrics, got %d", len(ch))
		}
	})

	t.Run("populated cache emits metrics", func(t *testing.T) {
		snap := &rbdCacheSnapshot{
			pools: map[poolNsKey]*rbdPoolData{
				{pool: "rbd-pool", radosNamespace: "ns1"}: {
					consumerName: "consumer-a",
					images: map[string]rbdImageData{
						"img-001": {pvName: "pvc-abc", children: 2},
						"img-002": {pvName: "pvc-def", children: 0},
					},
					mirrors: []rbdMirrorData{
						{imageName: "img-001", siteName: "site-b", state: 4},
					},
				},
			},
		}
		c.cache.Store(snap)

		ch := make(chan prometheus.Metric, 20)
		c.Collect(ch)
		close(ch)

		var metrics []prometheus.Metric
		for m := range ch {
			metrics = append(metrics, m)
		}

		// 2 images * 2 descs (pvMetadata + childrenCount) + 1 mirror = 5
		if len(metrics) != 5 {
			t.Fatalf("expected 5 metrics, got %d", len(metrics))
		}

		// Verify one of the metrics has the right label values.
		found := false
		for _, m := range metrics {
			var d dto.Metric
			if err := m.Write(&d); err != nil {
				t.Fatal(err)
			}
			for _, lp := range d.Label {
				if lp.GetName() == "site_name" && lp.GetValue() == "site-b" {
					found = true
					if d.Gauge.GetValue() != 4 {
						t.Errorf("mirror state = %v, want 4", d.Gauge.GetValue())
					}
				}
			}
		}
		if !found {
			t.Error("did not find mirror metric with site_name=site-b")
		}
	})
}

func TestCephRBDCollectorDescribe(t *testing.T) {
	c := &CephRBDCollector{
		pvMetadata: prometheus.NewDesc("a", "a", nil, nil),
		childrenCount: prometheus.NewDesc("b", "b", nil, nil),
		mirrorState: prometheus.NewDesc("c", "c", nil, nil),
	}

	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}
	if len(descs) != 3 {
		t.Errorf("expected 3 descriptors, got %d", len(descs))
	}
}

func makeImageNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("img-%04d", i)
	}
	return names
}

// Verify cache type satisfies atomic.Pointer usage.
func TestRBDCacheAtomicPointer(t *testing.T) {
	var p atomic.Pointer[rbdCacheSnapshot]
	if p.Load() != nil {
		t.Error("zero value should be nil")
	}
	snap := &rbdCacheSnapshot{pools: map[poolNsKey]*rbdPoolData{}}
	p.Store(snap)
	if p.Load() != snap {
		t.Error("stored value should be retrievable")
	}
}
