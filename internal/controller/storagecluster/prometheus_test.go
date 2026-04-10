package storagecluster

import (
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestFilterExcludedAlertsFromGroups(t *testing.T) {
	tests := []struct {
		name           string
		groups         []monitoringv1.RuleGroup
		excludedAlerts []string
		expected       []monitoringv1.RuleGroup
	}{
		{
			name: "no alerts excluded - return all rules",
			groups: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFNodeMTULessThan9000", Expr: intstr.FromString("expr2")},
						{Alert: "ODFDiskUtilizationHigh", Expr: intstr.FromString("expr3")},
					},
				},
			},
			excludedAlerts: []string{},
			expected: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFNodeMTULessThan9000", Expr: intstr.FromString("expr2")},
						{Alert: "ODFDiskUtilizationHigh", Expr: intstr.FromString("expr3")},
					},
				},
			},
		},
		{
			name: "disable one alert - remove from list",
			groups: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFNodeMTULessThan9000", Expr: intstr.FromString("expr2")},
						{Alert: "ODFDiskUtilizationHigh", Expr: intstr.FromString("expr3")},
					},
				},
			},
			excludedAlerts: []string{"ODFNodeMTULessThan9000"},
			expected: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFDiskUtilizationHigh", Expr: intstr.FromString("expr3")},
					},
				},
			},
		},
		{
			name: "disable multiple alerts - remove all matches",
			groups: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFNodeLatencyHighOnNonOSDNodes", Expr: intstr.FromString("expr2")},
						{Alert: "ODFNodeMTULessThan9000", Expr: intstr.FromString("expr3")},
						{Alert: "ODFNodeNICBandwidthSaturation", Expr: intstr.FromString("expr4")},
					},
				},
			},
			excludedAlerts: []string{
				"ODFNodeLatencyHighOnOSDNodes",
				"ODFNodeMTULessThan9000",
			},
			expected: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnNonOSDNodes", Expr: intstr.FromString("expr2")},
						{Alert: "ODFNodeNICBandwidthSaturation", Expr: intstr.FromString("expr4")},
					},
				},
			},
		},
		{
			name: "disable all alerts - return empty groups",
			groups: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
						{Alert: "ODFCorePodRestarted", Expr: intstr.FromString("expr2")},
					},
				},
			},
			excludedAlerts: []string{
				"ODFNodeLatencyHighOnOSDNodes",
				"ODFCorePodRestarted",
			},
			expected: []monitoringv1.RuleGroup{},
		},
		{
			name:           "empty input groups - return empty",
			groups:         []monitoringv1.RuleGroup{},
			excludedAlerts: []string{"any_alert"},
			expected:       []monitoringv1.RuleGroup{},
		},
		{
			name: "nil disabled alerts - return all rules",
			groups: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
					},
				},
			},
			excludedAlerts: nil,
			expected: []monitoringv1.RuleGroup{
				{
					Name: "odf_healthchecks.rules",
					Rules: []monitoringv1.Rule{
						{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterExcludedAlertsFromGroups(tt.groups, tt.excludedAlerts)
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("exclude then un-exclude alerts - alerts return when removed from exclusion list", func(t *testing.T) {
		originalGroups := []monitoringv1.RuleGroup{
			{
				Name: "odf_healthchecks.rules",
				Rules: []monitoringv1.Rule{
					{Alert: "ODFNodeLatencyHighOnOSDNodes", Expr: intstr.FromString("expr1")},
					{Alert: "ODFNodeMTULessThan9000", Expr: intstr.FromString("expr2")},
					{Alert: "ODFDiskUtilizationHigh", Expr: intstr.FromString("expr3")},
				},
			},
		}
		excludedAlerts := []string{"ODFNodeMTULessThan9000", "ODFDiskUtilizationHigh"}

		// Step 1: Exclude alerts → verify they're removed
		filtered := filterExcludedAlertsFromGroups(originalGroups, excludedAlerts)
		assert.Len(t, filtered, 1)
		assert.Len(t, filtered[0].Rules, 1)
		assert.Equal(t, "ODFNodeLatencyHighOnOSDNodes", filtered[0].Rules[0].Alert)

		// Step 2: "Un-exclude" by removing from exclusion list → verify alerts return
		// Use the ORIGINAL groups (not the filtered output) with empty exclusion list
		unfiltered := filterExcludedAlertsFromGroups(originalGroups, []string{})
		assert.Equal(t, originalGroups, unfiltered)
	})
}
