package storagecluster

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGenerateVaultAgentConfig(t *testing.T) {
	t.Run("Basic config without TLS", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"KMS_PROVIDER":          "vault",
				"VAULT_ADDR":            "https://vault.example.com:8200",
				"VAULT_RGW_AUTH_METHOD": "agent",
			},
		}

		config := generateVaultAgentConfig(kmsConfigMap)

		assert.Contains(t, config, "disable_mlock = true")
		assert.Contains(t, config, `address = "https://vault.example.com:8200"`)
		assert.Contains(t, config, `mount_path = "auth/kubernetes"`)
		assert.Contains(t, config, `role = "rook-ceph-rgw"`)
		assert.Contains(t, config, "use_auto_auth_token = true")
		assert.Contains(t, config, "0.0.0.0:8100")
		assert.Contains(t, config, "tls_disable = true")
		assert.NotContains(t, config, "tls_config")
	})

	t.Run("Config with custom role and auth mount path", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_ADDR":                "https://vault.example.com:8200",
				"VAULT_RGW_ROLE":            "custom-rgw-role",
				"VAULT_RGW_AUTH_MOUNT_PATH": "auth/k8s-cluster1",
			},
		}

		config := generateVaultAgentConfig(kmsConfigMap)

		assert.Contains(t, config, `role = "custom-rgw-role"`)
		assert.Contains(t, config, `mount_path = "auth/k8s-cluster1"`)
	})

	t.Run("Config with TLS CA cert only", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_ADDR":   "https://vault.example.com:8200",
				"VAULT_CACERT": "vault-ca-secret",
			},
		}

		config := generateVaultAgentConfig(kmsConfigMap)

		assert.Contains(t, config, "tls_config")
		assert.Contains(t, config, `ca_cert`)
		assert.Contains(t, config, "/vault/tls/ca/cert")
		assert.NotContains(t, config, "client_cert")
		assert.NotContains(t, config, "client_key")
	})

	t.Run("Config with full TLS", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_ADDR":        "https://vault.example.com:8200",
				"VAULT_CACERT":      "vault-ca-secret",
				"VAULT_CLIENT_CERT": "vault-client-cert-secret",
				"VAULT_CLIENT_KEY":  "vault-client-key-secret",
			},
		}

		config := generateVaultAgentConfig(kmsConfigMap)

		assert.Contains(t, config, "tls_config")
		assert.Contains(t, config, "/vault/tls/ca/cert")
		assert.Contains(t, config, "/vault/tls/client-cert/cert")
		assert.Contains(t, config, "/vault/tls/client-key/key")
	})
}

func TestGenerateVaultAgentTLSConfig(t *testing.T) {
	t.Run("No TLS secrets returns empty string", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_ADDR": "https://vault.example.com:8200",
			},
		}
		result := generateVaultAgentTLSConfig(kmsConfigMap)
		assert.Empty(t, result)
	})

	t.Run("CA cert only", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_CACERT": "my-ca-secret",
			},
		}
		result := generateVaultAgentTLSConfig(kmsConfigMap)
		assert.Contains(t, result, "ca_cert")
		assert.NotContains(t, result, "client_cert")
		assert.NotContains(t, result, "client_key")
	})
}

func TestGetVaultAgentTLSVolumes(t *testing.T) {
	t.Run("No TLS secrets returns empty slices", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_ADDR": "https://vault.example.com:8200",
			},
		}
		volumes, mounts := getVaultAgentTLSVolumes(kmsConfigMap)
		assert.Empty(t, volumes)
		assert.Empty(t, mounts)
	})

	t.Run("All TLS secrets present", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_CACERT":      "ca-secret",
				"VAULT_CLIENT_CERT": "cert-secret",
				"VAULT_CLIENT_KEY":  "key-secret",
			},
		}
		volumes, mounts := getVaultAgentTLSVolumes(kmsConfigMap)
		assert.Len(t, volumes, 3)
		assert.Len(t, mounts, 3)

		// Verify secret names are used
		assert.Equal(t, "ca-secret", volumes[0].Secret.SecretName)
		assert.Equal(t, "cert-secret", volumes[1].Secret.SecretName)
		assert.Equal(t, "key-secret", volumes[2].Secret.SecretName)

		// Verify mount paths
		assert.Equal(t, vaultAgentTLSCAMountPath, mounts[0].MountPath)
		assert.Equal(t, vaultAgentTLSCertMountPath, mounts[1].MountPath)
		assert.Equal(t, vaultAgentTLSKeyMountPath, mounts[2].MountPath)

		// All mounts should be read-only
		for _, m := range mounts {
			assert.True(t, m.ReadOnly)
		}
	})

	t.Run("Only CA cert present", func(t *testing.T) {
		kmsConfigMap := &corev1.ConfigMap{
			Data: map[string]string{
				"VAULT_CACERT": "ca-secret",
			},
		}
		volumes, mounts := getVaultAgentTLSVolumes(kmsConfigMap)
		assert.Len(t, volumes, 1)
		assert.Len(t, mounts, 1)
		assert.Equal(t, "ca-secret", volumes[0].Secret.SecretName)
	})
}

func TestVaultAgentServiceURL(t *testing.T) {
	url := VaultAgentServiceURL("openshift-storage")
	assert.Equal(t, "http://vault-agent-rgw.openshift-storage.svc:8100", url)

	url = VaultAgentServiceURL("")
	assert.True(t, strings.Contains(url, "vault-agent-rgw"))
	assert.True(t, strings.Contains(url, "8100"))
}
