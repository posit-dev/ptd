package steps

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	k8syamlv2 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/yaml/v2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// helmChartsCRDSpec is the CRD spec for helmcharts.helm.cattle.io (HelmController CRD),
// shared between AWS and Azure clusters implementations.
var helmChartsCRDSpec = map[string]interface{}{
	"group":                 "helm.cattle.io",
	"preserveUnknownFields": false,
	"scope":                 "Namespaced",
	"names": map[string]interface{}{
		"kind":     "HelmChart",
		"plural":   "helmcharts",
		"singular": "helmchart",
	},
	"versions": []interface{}{
		map[string]interface{}{
			"name":    "v1",
			"served":  true,
			"storage": true,
			"subresources": map[string]interface{}{
				"status": map[string]interface{}{},
			},
			"additionalPrinterColumns": []interface{}{
				map[string]interface{}{"jsonPath": ".status.jobName", "name": "Job", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.chart", "name": "Chart", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.targetNamespace", "name": "TargetNamespace", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.version", "name": "Version", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.repo", "name": "Repo", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.helmVersion", "name": "HelmVersion", "type": "string"},
				map[string]interface{}{"jsonPath": ".spec.bootstrap", "name": "Bootstrap", "type": "string"},
			},
			"schema": map[string]interface{}{
				"openAPIV3Schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"spec": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"authPassCredentials":   map[string]interface{}{"type": "boolean"},
								"authSecret":            map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
								"backOffLimit":          map[string]interface{}{"nullable": true, "type": "integer"},
								"bootstrap":             map[string]interface{}{"type": "boolean"},
								"chart":                 map[string]interface{}{"nullable": true, "type": "string"},
								"chartContent":          map[string]interface{}{"nullable": true, "type": "string"},
								"createNamespace":       map[string]interface{}{"type": "boolean"},
								"dockerRegistrySecret":  map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
								"failurePolicy":         map[string]interface{}{"nullable": true, "type": "string"},
								"helmVersion":           map[string]interface{}{"nullable": true, "type": "string"},
								"insecureSkipTLSVerify": map[string]interface{}{"type": "boolean"},
								"jobImage":              map[string]interface{}{"nullable": true, "type": "string"},
								"plainHTTP":             map[string]interface{}{"type": "boolean"},
								"podSecurityContext": map[string]interface{}{
									"nullable": true,
									"type":     "object",
									"properties": map[string]interface{}{
										"fsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
										"fsGroupChangePolicy": map[string]interface{}{"nullable": true, "type": "string"},
										"runAsGroup":          map[string]interface{}{"nullable": true, "type": "integer"},
										"runAsNonRoot":        map[string]interface{}{"nullable": true, "type": "boolean"},
										"runAsUser":           map[string]interface{}{"nullable": true, "type": "integer"},
										"seLinuxOptions": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"level": map[string]interface{}{"nullable": true, "type": "string"},
												"role":  map[string]interface{}{"nullable": true, "type": "string"},
												"type":  map[string]interface{}{"nullable": true, "type": "string"},
												"user":  map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"seccompProfile": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
												"type":             map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"supplementalGroups": map[string]interface{}{
											"nullable": true, "type": "array",
											"items": map[string]interface{}{"type": "integer"},
										},
										"sysctls": map[string]interface{}{
											"nullable": true, "type": "array",
											"items": map[string]interface{}{
												"type": "object",
												"properties": map[string]interface{}{
													"name":  map[string]interface{}{"nullable": true, "type": "string"},
													"value": map[string]interface{}{"nullable": true, "type": "string"},
												},
											},
										},
										"windowsOptions": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
												"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
												"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
												"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
								"repo":            map[string]interface{}{"nullable": true, "type": "string"},
								"repoCA":          map[string]interface{}{"nullable": true, "type": "string"},
								"repoCAConfigMap": map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
								"securityContext": map[string]interface{}{
									"nullable": true,
									"type":     "object",
									"properties": map[string]interface{}{
										"allowPrivilegeEscalation": map[string]interface{}{"nullable": true, "type": "boolean"},
										"capabilities": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"add":  map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
												"drop": map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
											},
										},
										"privileged":             map[string]interface{}{"nullable": true, "type": "boolean"},
										"procMount":              map[string]interface{}{"nullable": true, "type": "string"},
										"readOnlyRootFilesystem": map[string]interface{}{"nullable": true, "type": "boolean"},
										"runAsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
										"runAsNonRoot":           map[string]interface{}{"nullable": true, "type": "boolean"},
										"runAsUser":              map[string]interface{}{"nullable": true, "type": "integer"},
										"seLinuxOptions": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"level": map[string]interface{}{"nullable": true, "type": "string"},
												"role":  map[string]interface{}{"nullable": true, "type": "string"},
												"type":  map[string]interface{}{"nullable": true, "type": "string"},
												"user":  map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"seccompProfile": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
												"type":             map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"windowsOptions": map[string]interface{}{
											"nullable": true, "type": "object",
											"properties": map[string]interface{}{
												"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
												"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
												"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
												"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
								"set":             map[string]interface{}{"nullable": true, "type": "object", "additionalProperties": map[string]interface{}{"x-kubernetes-int-or-string": true}},
								"targetNamespace": map[string]interface{}{"nullable": true, "type": "string"},
								"timeout":         map[string]interface{}{"nullable": true, "type": "string"},
								"valuesContent":   map[string]interface{}{"nullable": true, "type": "string"},
								"version":         map[string]interface{}{"nullable": true, "type": "string"},
							},
						},
						"status": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"conditions": map[string]interface{}{
									"nullable": true, "type": "array",
									"items": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"message": map[string]interface{}{"nullable": true, "type": "string"},
											"reason":  map[string]interface{}{"nullable": true, "type": "string"},
											"status":  map[string]interface{}{"nullable": true, "type": "string"},
											"type":    map[string]interface{}{"nullable": true, "type": "string"},
										},
									},
								},
								"jobName": map[string]interface{}{"nullable": true, "type": "string"},
							},
						},
					},
				},
			},
		},
	},
}

// helmChartConfigsCRDSpec is the CRD spec for helmchartconfigs.helm.cattle.io (HelmController CRD),
// shared between AWS and Azure clusters implementations.
var helmChartConfigsCRDSpec = map[string]interface{}{
	"group":                 "helm.cattle.io",
	"preserveUnknownFields": false,
	"scope":                 "Namespaced",
	"names": map[string]interface{}{
		"kind":     "HelmChartConfig",
		"plural":   "helmchartconfigs",
		"singular": "helmchartconfig",
	},
	"versions": []interface{}{
		map[string]interface{}{
			"name":    "v1",
			"served":  true,
			"storage": true,
			"schema": map[string]interface{}{
				"openAPIV3Schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"spec": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"failurePolicy": map[string]interface{}{"nullable": true, "type": "string"},
								"valuesContent": map[string]interface{}{"nullable": true, "type": "string"},
							},
						},
					},
				},
			},
		},
	},
}

// buildAWSClustersResourceTags returns the AWS resource tags matching Python's AWSWorkloadClusters.required_tags:
//
//	workload.required_tags | { "posit.team/managed-by": "ptd.pulumi_resources.aws_workload_clusters" }
//
// where workload.required_tags = resource_tags | { "posit.team/true-name": trueName, "posit.team/environment": environment }.
// AWS uses "/" separators (not ":" like Azure), does NOT include Owner: "ptd".
func buildAWSClustersResourceTags(compoundName string, resourceTags map[string]string) pulumi.StringMap {
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	tags := pulumi.StringMap{}
	for k, v := range resourceTags {
		tags[k] = pulumi.String(v)
	}
	tags["posit.team/true-name"] = pulumi.String(trueName)
	tags["posit.team/environment"] = pulumi.String(environment)
	tags["posit.team/managed-by"] = pulumi.String("ptd.pulumi_resources.aws_workload_clusters")
	return tags
}

// buildAzureRequiredTags returns the Azure "required_tags" equivalent used by Python's AzureWorkload.required_tags.
// Python sets posit.team:true-name and posit.team:environment (via azure_tag_key_format which converts "/" to ":")
// derived by splitting compound_name on the last "-". Does NOT include Owner: "ptd".
func buildAzureRequiredTags(compoundName string, resourceTags map[string]string) pulumi.StringMap {
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	tags := pulumi.StringMap{}
	for k, v := range resourceTags {
		tags[k] = pulumi.String(v)
	}
	tags["posit.team:true-name"] = pulumi.String(trueName)
	tags["posit.team:environment"] = pulumi.String(environment)
	return tags
}

// buildResourceTagsWithExtra returns a pulumi.StringMap combining base resourceTags with extra tags.
func buildResourceTagsWithExtra(resourceTags map[string]string, extra map[string]string) pulumi.StringMap {
	m := pulumi.StringMap{}
	for k, v := range resourceTags {
		m[k] = pulumi.String(v)
	}
	for k, v := range extra {
		m[k] = pulumi.String(v)
	}
	return m
}

// resolveTeamOperatorImage picks the effective image string from adhoc/regular overrides,
// then parses it into (repository, tag) for use in Helm values.
// Returns ("", "") when no override is configured (chart uses its default appVersion).
// Mirrors Python's TeamOperator._define_image / _define_helm_release logic.
func resolveTeamOperatorImage(adhocImage, regularImage *string) (repository, tag string) {
	var imageStr string
	if adhocImage != nil && *adhocImage != "" {
		imageStr = *adhocImage
	} else if regularImage != nil && *regularImage != "" {
		imageStr = *regularImage
	}
	if imageStr == "" {
		return "", ""
	}
	// Digest form: "hostname/repo@sha256:abc123"
	if idx := strings.LastIndex(imageStr, "@"); idx >= 0 {
		return imageStr[:idx], imageStr[idx:] // tag keeps the "@" prefix for Helm
	}
	// Tag form: "hostname/repo:tag" — split on last ":" in the last path segment only.
	lastSlash := strings.LastIndex(imageStr, "/")
	lastPart := imageStr[lastSlash+1:]
	if colonIdx := strings.LastIndex(lastPart, ":"); colonIdx >= 0 {
		splitAt := lastSlash + 1 + colonIdx
		return imageStr[:splitAt], imageStr[splitAt+1:]
	}
	// No tag specified
	return imageStr, "latest"
}

// createCustomK8sResources applies custom Kubernetes resources from the workload's
// custom_k8s_resources/ directory. For each named subfolder, all YAML files are applied
// in alphabetical order using kubernetes:yaml/v2:ConfigFile resources.
//
// Mirrors Python's apply_custom_k8s_resources in custom_k8s_resources.py.
// Logical name per file: "{release}-custom-{subfolder}-{stem}".
func createCustomK8sResources(
	ctx *pulumi.Context,
	workloadDir string,
	release string,
	subfolders []string,
	k8sProviderOpt pulumi.ResourceOption,
	aliasOpt pulumi.ResourceOption,
) error {
	if len(subfolders) == 0 {
		return nil
	}
	customResourcesDir := filepath.Join(workloadDir, "custom_k8s_resources")
	if _, err := os.Stat(customResourcesDir); os.IsNotExist(err) {
		return nil
	}

	for _, subfolder := range subfolders {
		subfolderPath := filepath.Join(customResourcesDir, subfolder)
		info, err := os.Stat(subfolderPath)
		if os.IsNotExist(err) {
			return fmt.Errorf("custom_k8s_resources: subfolder not found: %s", subfolderPath)
		}
		if err != nil {
			return fmt.Errorf("custom_k8s_resources: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("custom_k8s_resources: path is not a directory: %s", subfolderPath)
		}

		entries, err := os.ReadDir(subfolderPath)
		if err != nil {
			return fmt.Errorf("custom_k8s_resources: failed to read %s: %w", subfolderPath, err)
		}

		var yamlFiles []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				yamlFiles = append(yamlFiles, filepath.Join(subfolderPath, name))
			}
		}
		sort.Strings(yamlFiles)

		for _, yamlFile := range yamlFiles {
			stem := strings.TrimSuffix(filepath.Base(yamlFile), filepath.Ext(yamlFile))
			logicalName := fmt.Sprintf("%s-custom-%s-%s", release, subfolder, stem)
			if _, err := k8syamlv2.NewConfigFile(ctx, logicalName, &k8syamlv2.ConfigFileArgs{
				File: pulumi.String(yamlFile),
			}, k8sProviderOpt, aliasOpt); err != nil {
				return fmt.Errorf("custom_k8s_resources: failed to apply %s: %w", yamlFile, err)
			}
		}
	}
	return nil
}
