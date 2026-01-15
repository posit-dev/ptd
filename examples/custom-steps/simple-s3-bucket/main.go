package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/rstudio/ptd/lib/helpers"
	"github.com/rstudio/ptd/lib/types"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Get PTD config
		ptdCfg := config.New(ctx, "ptd")
		yamlPath := ptdCfg.Require("ptdYamlPath")

		workloadCfg, err := helpers.LoadPtdYaml(yamlPath)
		if err != nil {
			return fmt.Errorf("loading ptd.yaml: %w", err)
		}

		workload, ok := workloadCfg.(types.AWSWorkloadConfig)
		if !ok {
			return fmt.Errorf("invalid workload config type")
		}

		// Get resource tags for workload
		requiredTags := pulumi.StringMap{}
		for k, v := range workload.ResourceTags {
			requiredTags[k] = pulumi.String(v)
		}

		// Create a bucket with tags from ptd.yaml plus resource-specific tags
		bucket, err := s3.NewBucketV2(ctx, "ptd-custom-data-bucket", &s3.BucketV2Args{
			Tags: requiredTags.ToStringMapOutput().ApplyT(func(tags map[string]string) pulumi.StringMap {
				result := pulumi.StringMap{}
				// Copy all resource tags from config
				for k, v := range tags {
					result[k] = pulumi.String(v)
				}
				// Add resource-specific tags
				result["Name"] = pulumi.String("ptd-custom-data-bucket")
				result["Purpose"] = pulumi.String("Custom Step Example")
				result["Managed"] = pulumi.String("PTD Custom Step")
				return result
			}).(pulumi.StringMapOutput),
		})
		if err != nil {
			return err
		}

		ctx.Export("bucketArn", bucket.Arn)

		return nil
	})
}
