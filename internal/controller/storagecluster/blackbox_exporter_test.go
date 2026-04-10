// blackbox_exporter_test.go
package storagecluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/client/clientset/versioned/scheme"
	securityv1 "github.com/openshift/api/security/v1"
	fakeclientset "github.com/openshift/client-go/security/clientset/versioned/fake"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	ocsv1 "github.com/red-hat-storage/ocs-operator/api/v4/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	// Add required schemes
	_ = clientgoscheme.AddToScheme(scheme.Scheme)
	_ = monitoringv1.AddToScheme(scheme.Scheme)
	_ = securityv1.Install(scheme.Scheme)
	_ = ocsv1.AddToScheme(scheme.Scheme)
}

func TestCreateBlackboxServiceAccount(t *testing.T) {
	scheme := scheme.Scheme
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &StorageClusterReconciler{
		Client: client,
		Log:    log,
	}

	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}

	err := reconciler.createBlackboxServiceAccount(context.TODO(), instance)
	require.NoError(t, err)

	sa := &corev1.ServiceAccount{}
	err = client.Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-storage",
		Name:      blackboxServiceAccount,
	}, sa)
	require.NoError(t, err)
	assert.Equal(t, blackboxExporterLabels, sa.Labels)
	assert.Len(t, sa.OwnerReferences, 1)
}

func TestCreateBlackboxSCC(t *testing.T) {
	kubeClient := fake.NewClientBuilder().Build()

	securityClient := fakeclientset.NewClientset()

	reconciler := &StorageClusterReconciler{
		Client:         kubeClient,
		SecurityClient: securityClient.SecurityV1(),
		Log:            log,
	}

	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}

	err := reconciler.createBlackboxSCC(context.TODO(), instance)
	require.NoError(t, err)

	// Retrieve the created SCC from the fake client
	scc, err := securityClient.SecurityV1().SecurityContextConstraints().Get(context.TODO(), blackboxSCCName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, blackboxSCCName, scc.Name)
}

func TestCreateBlackboxConfigMap(t *testing.T) {
	scheme := scheme.Scheme
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &StorageClusterReconciler{
		Client: client,
		Log:    log,
		Scheme: scheme,
	}

	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}

	err := reconciler.createBlackboxConfigMap(context.TODO(), instance)
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	err = client.Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-storage",
		Name:      blackboxConfigMapName,
	}, cm)
	require.NoError(t, err)
	assert.Contains(t, cm.Data["config.yml"], "icmp_internal")
	assert.Equal(t, blackboxExporterLabels, cm.Labels)
}

func TestCreateBlackboxDeployment(t *testing.T) {

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = monitoringv1.AddToScheme(scheme)
	_ = ocsv1.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &StorageClusterReconciler{
		Client: client,
		Log:    log,
		Scheme: scheme,
		images: ImageMap{
			BlackboxExporter: "quay.io/prometheus/blackbox-exporter:v0.25.0",
		},
	}

	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}

	err := reconciler.createBlackboxDeployment(context.TODO(), instance)
	require.NoError(t, err)

	deployment := &appsv1.Deployment{}
	err = client.Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-storage",
		Name:      blackboxExporterName,
	}, deployment)
	require.NoError(t, err)

	container := deployment.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "quay.io/prometheus/blackbox-exporter:v0.25.0", container.Image)
	assert.Equal(t, "NET_RAW", string(container.SecurityContext.Capabilities.Add[0]))
	assert.True(t, *container.SecurityContext.RunAsNonRoot)
	assert.Equal(t, "0 2147483647", deployment.Spec.Template.Spec.SecurityContext.Sysctls[0].Value)
}

func TestCreateBlackboxService(t *testing.T) {
	scheme := scheme.Scheme
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &StorageClusterReconciler{
		Client: client,
		Log:    log,
		Scheme: scheme,
	}

	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}

	err := reconciler.createBlackboxService(context.TODO(), instance)
	require.NoError(t, err)

	svc := &corev1.Service{}
	err = client.Get(context.TODO(), types.NamespacedName{
		Namespace: "openshift-storage",
		Name:      blackboxExporterName,
	}, svc)
	require.NoError(t, err)
	assert.Equal(t, int32(9115), svc.Spec.Ports[0].Port)
	assert.Equal(t, blackboxExporterLabels, svc.Spec.Selector)
}

func TestCreateBlackboxProbe(t *testing.T) {
	instance := &ocsv1.StorageCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ocs-storagecluster",
			Namespace: "openshift-storage",
		},
	}
	nodeIPs := []string{"10.0.1.10"}

	probe := getDesiredProbe(instance, nodeIPs)

	assert.Equal(t, "odf-blackbox-exporter.openshift-storage.svc:9115", probe.Spec.ProberSpec.URL)
	require.NotNil(t, probe.Spec.Targets.StaticConfig)
	assert.Contains(t, probe.Spec.Targets.StaticConfig.Targets, "10.0.1.10")
	assert.Equal(t, "icmp_internal", probe.Spec.Module)
}

func getDesiredProbe(instance *ocsv1.StorageCluster, nodeIPs []string) *monitoringv1.Probe {
	return &monitoringv1.Probe{
		ObjectMeta: metav1.ObjectMeta{
			Name:      blackboxExporterName,
			Namespace: instance.Namespace,
			Labels:    blackboxExporterLabels,
		},
		Spec: monitoringv1.ProbeSpec{
			ProberSpec: monitoringv1.ProberSpec{
				URL: fmt.Sprintf("%s.%s.svc:%d", blackboxExporterName, instance.Namespace, blackboxPortNumber),
			},
			Module: "icmp_internal",
			Targets: monitoringv1.ProbeTargets{
				StaticConfig: &monitoringv1.ProbeTargetStaticConfig{
					Targets: nodeIPs,
					Labels:  map[string]string{"job": blackboxExporterName},
				},
			},
			Interval:      blackboxScrapeInterval,
			ScrapeTimeout: "5s",
		},
	}
}

// TestBuildIPRegex verifies that buildIPRegex generates the correct regex string
func TestBuildIPRegex(t *testing.T) {
	tests := []struct {
		name     string
		ips      []string
		expected string
	}{
		{
			name:     "empty list returns never-match regex",
			ips:      []string{},
			expected: "$^",
		},
		{
			name:     "single IPv4 address",
			ips:      []string{"10.0.0.1"},
			expected: "^(10\\.0\\.0\\.1)$",
		},
		{
			name:     "multiple IPv4 addresses",
			ips:      []string{"10.128.2.82", "10.129.2.60", "10.131.0.49"},
			expected: "^(10\\.128\\.2\\.82|10\\.129\\.2\\.60|10\\.131\\.0\\.49)$",
		},
		{
			name:     "IPv6 standard format",
			ips:      []string{"2001:db8::1"},
			expected: "^(2001:db8::1)$",
		},
		{
			name:     "IPv6 loopback",
			ips:      []string{"::1"},
			expected: "^(::1)$",
		},
		{
			name:     "multiple IPv6 addresses",
			ips:      []string{"fe80::1", "2001:db8::1", "::1"},
			expected: "^(fe80::1|2001:db8::1|::1)$",
		},
		{
			name:     "mixed IPv4 and IPv6",
			ips:      []string{"10.0.0.1", "2001:db8::1"},
			expected: "^(10\\.0\\.0\\.1|2001:db8::1)$",
		},
		{
			name:     "IPv4 with various octets",
			ips:      []string{"192.168.1.1", "10.255.255.255", "0.0.0.0"},
			expected: "^(192\\.168\\.1\\.1|10\\.255\\.255\\.255|0\\.0\\.0\\.0)$",
		},
		{
			name:     "IPv6 with zone ID",
			ips:      []string{"fe80::1%eth0"},
			expected: "^(fe80::1%eth0)$",
		},
		{
			name:     "single digit octets",
			ips:      []string{"1.2.3.4"},
			expected: "^(1\\.2\\.3\\.4)$",
		},
		{
			name:     "large IPv6 address",
			ips:      []string{"2001:0db8:85a3:0000:0000:8a2e:0370:7334"},
			expected: "^(2001:0db8:85a3:0000:0000:8a2e:0370:7334)$",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildIPRegex(tt.ips)
			if got != tt.expected {
				t.Errorf("buildIPRegex(%q) = %q, want %q", tt.ips, got, tt.expected)
			}
		})
	}
}
