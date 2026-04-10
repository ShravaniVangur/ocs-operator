package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	prometheusconfig "github.com/prometheus/common/config"
	pb "github.com/red-hat-storage/ocs-operator/services/provider/api/v4"
	"k8s.io/klog/v2"
)

const (
	alertsClientTimeout      = 30 * time.Second
	storageConsumerNameLabel = "consumer_name"
)

// prometheusAlertsResponse represents the response from the Prometheus /api/v1/alerts endpoint
type prometheusAlertsResponse struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []prometheusAlert `json:"alerts"`
	} `json:"data"`
}

type prometheusAlert struct {
	Labels map[string]string `json:"labels"`
	State  string            `json:"state"`
	Value  string            `json:"value"`
}

// newPrometheusHTTPClient creates a default HTTP client configured for in-cluster
// Prometheus access using the service account credentials.
func newPrometheusHTTPClient() (*http.Client, error) {
	secrets := "/var/run/secrets/kubernetes.io/serviceaccount"
	caCertPath := secrets + "/service-ca.crt"
	tokenPath := secrets + "/token"

	httpConfig := prometheusconfig.HTTPClientConfig{
		Authorization: &prometheusconfig.Authorization{
			Type:            "Bearer",
			CredentialsFile: tokenPath,
		},
		TLSConfig: prometheusconfig.TLSConfig{
			CAFile: caCertPath,
		},
		FollowRedirects: true,
		EnableHTTP2:     true,
	}

	client, err := prometheusconfig.NewClientFromConfig(
		httpConfig,
		"ocs-provider-prometheus",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus HTTP client: %w", err)
	}

	client.Timeout = alertsClientTimeout

	return client, nil
}

// alertStore maintains a per-consumer cache of alerts, updated by a background poller.
type alertStore struct {
	httpClient   *http.Client
	promURL      string
	pollInterval time.Duration

	mu               sync.RWMutex
	alertsByConsumer map[string][]*pb.AlertInfo
	lastUpdateTime   time.Time
}

// newAlertStore creates an alert poller. Call startPolling() to begin background updates.
func newAlertStore(promURL string, pollInterval time.Duration, httpClient *http.Client) *alertStore {
	return &alertStore{
		httpClient:       httpClient,
		promURL:          promURL,
		pollInterval:     pollInterval,
		alertsByConsumer: make(map[string][]*pb.AlertInfo),
	}
}

// startPolling begins background polling. Runs until context is cancelled.
func (c *alertStore) startPolling(ctx context.Context) {
	logger := klog.FromContext(ctx).WithName("alert")
	logger.Info("Starting alert cache polling", "interval", c.pollInterval, "prometheusURL", c.promURL)

	// Initial fetch immediately
	if err := c.fetchAndUpdate(ctx); err != nil {
		logger.Error(err, "Initial alert fetch failed (will retry)")
	}

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping alert cache polling")
			return
		case <-ticker.C:
			if err := c.fetchAndUpdate(ctx); err != nil {
				logger.Error(err, "Failed to fetch alerts from Prometheus")
			}
		}
	}
}

// fetchAndUpdate queries Prometheus and updates the cache.
func (c *alertStore) fetchAndUpdate(ctx context.Context) error {
	logger := klog.FromContext(ctx).WithName("fetchAndUpdate")

	alertsURL := fmt.Sprintf("%s/api/v1/alerts", c.promURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, alertsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("prometheus returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var alertsResp prometheusAlertsResponse
	if err := json.Unmarshal(body, &alertsResp); err != nil {
		return fmt.Errorf("failed to unmarshal alerts: %w", err)
	}

	alertsByConsumer := buildConsumerMap(&alertsResp)

	c.mu.Lock()
	c.alertsByConsumer = alertsByConsumer
	c.lastUpdateTime = time.Now()
	c.mu.Unlock()

	duration := time.Since(startTime)
	logger.V(1).Info("Updated alert cache",
		"duration", duration,
		"totalAlerts", len(alertsResp.Data.Alerts),
		"consumersWithAlerts", len(alertsByConsumer),
	)

	return nil
}

// buildConsumerMap organizes firing alerts by consumer name.
func buildConsumerMap(alertsResp *prometheusAlertsResponse) map[string][]*pb.AlertInfo {
	result := make(map[string][]*pb.AlertInfo)

	for i := range alertsResp.Data.Alerts {
		alert := &alertsResp.Data.Alerts[i]

		if alert.State != "firing" {
			continue
		}

		consumerName := alert.Labels[storageConsumerNameLabel]
		if consumerName == "" {
			continue
		}

		var value float64
		if alert.Value != "" {
			if v, err := strconv.ParseFloat(alert.Value, 64); err == nil {
				value = v
			}
		}

		result[consumerName] = append(result[consumerName], &pb.AlertInfo{
			AlertName: alert.Labels["alertname"],
			Labels:    alert.Labels,
			Value:     value,
		})
	}

	return result
}

// getAlertsForConsumer returns cached alerts for a specific consumer.
// If the cache hasn't been updated within 2x the poll interval, an error is returned.
// This can happen due to Prometheus connectivity issues or a programming error,
// in which case the client will be unaware of any active alerts.
func (c *alertStore) getAlertsForConsumer(consumerName string) ([]*pb.AlertInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Since(c.lastUpdateTime) > 2*c.pollInterval {
		return nil, fmt.Errorf("alert cache is stale: last update was %s ago", time.Since(c.lastUpdateTime))
	}

	alerts := c.alertsByConsumer[consumerName]
	if len(alerts) < 1 {
		return []*pb.AlertInfo{}, nil
	}

	result := make([]*pb.AlertInfo, len(alerts))
	copy(result, alerts)

	return result, nil
}
