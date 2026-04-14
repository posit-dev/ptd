package steps

import (
	"strings"

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
