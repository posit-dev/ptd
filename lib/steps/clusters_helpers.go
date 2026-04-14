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
