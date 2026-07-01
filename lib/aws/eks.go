package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func GetClusterEndpoint(ctx context.Context, c *Credentials, region string, clusterName string) (string, error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", err
	}

	return *output.Cluster.Endpoint, nil
}

// GetClusterInfo retrieves the endpoint, certificate authority data, and OIDC issuer URL for an EKS cluster.
func GetClusterInfo(ctx context.Context, c *Credentials, region string, clusterName string) (endpoint string, caCert string, oidcIssuerURL string, err error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", "", "", err
	}

	// Handle nil pointer dereference if CertificateAuthority is nil
	if output.Cluster == nil || output.Cluster.CertificateAuthority == nil {
		return "", "", "", nil
	}

	endpoint = ""
	if output.Cluster.Endpoint != nil {
		endpoint = *output.Cluster.Endpoint
	}

	caCert = ""
	if output.Cluster.CertificateAuthority.Data != nil {
		caCert = *output.Cluster.CertificateAuthority.Data
	}

	oidcIssuerURL = ""
	if output.Cluster.Identity != nil && output.Cluster.Identity.Oidc != nil && output.Cluster.Identity.Oidc.Issuer != nil {
		oidcIssuerURL = *output.Cluster.Identity.Oidc.Issuer
	}

	return endpoint, caCert, oidcIssuerURL, nil
}

// GetClusterAuthMode returns whether the named EKS cluster exists and, if so,
// its current authentication mode (CONFIG_MAP / API / API_AND_CONFIG_MAP).
//
// It replicates the boto3 describe_cluster auth-mode probe in
// python-pulumi/src/ptd/pulumi_resources/aws_eks_cluster.py __init__: when the
// cluster already exists the Python code does NOT set access_config on the
// Pulumi resource, so the live authenticationMode is preserved and the cluster
// is not replaced. Only a brand-new cluster is created with API_AND_CONFIG_MAP.
//
// Returns (exists=false, authMode="", nil) when the cluster does not exist yet
// (greenfield). A non-ResourceNotFound describe error is swallowed and treated
// as "does not exist" to match Python's defensive behaviour.
func GetClusterAuthMode(ctx context.Context, c *Credentials, region, clusterName string) (exists bool, authMode string, err error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, derr := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if derr != nil {
		// Match Python: ResourceNotFoundException → greenfield; any other error
		// is logged-and-ignored by the caller and treated as "does not exist".
		if strings.Contains(derr.Error(), "ResourceNotFound") {
			return false, "", nil
		}
		return false, "", nil
	}

	authMode = "CONFIG_MAP"
	if output.Cluster != nil && output.Cluster.AccessConfig != nil {
		if m := string(output.Cluster.AccessConfig.AuthenticationMode); m != "" {
			authMode = m
		}
	}
	return true, authMode, nil
}

// GetEKSToken generates an EKS-compatible token using STS presigned URLs
func GetEKSToken(ctx context.Context, c *Credentials, region string, clusterName string) (string, error) {
	// Create STS client with credentials
	stsClient := sts.New(sts.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	// Create presign client
	presigner := sts.NewPresignClient(stsClient)

	// Presign GetCallerIdentity with custom header for EKS
	presignedReq, err := presigner.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}, func(po *sts.PresignOptions) {
		po.ClientOptions = append(po.ClientOptions, sts.WithAPIOptions(
			smithyhttp.AddHeaderValue("x-k8s-aws-id", clusterName),
			addExpiresQueryParam(60),
		))
	})
	if err != nil {
		return "", err
	}

	// Base64url-encode the presigned URL (no padding)
	token := "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(presignedReq.URL))

	return token, nil
}

// ClusterVPCConfig holds the VPC-related configuration for an EKS cluster.
type ClusterVPCConfig struct {
	SubnetIDs        []string
	SecurityGroupIDs []string // includes clusterSecurityGroupId
	VpcID            string
	// ClusterSecurityGroupID is the EKS-managed cluster security group id (the
	// _setup_sg_access ingress-rule target). Empty when not reported.
	ClusterSecurityGroupID string
}

// GetClusterVPCConfig returns the VPC configuration for an EKS cluster by calling DescribeCluster.
func GetClusterVPCConfig(ctx context.Context, c *Credentials, region string, clusterName string) (ClusterVPCConfig, error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return ClusterVPCConfig{}, fmt.Errorf("failed to describe cluster %s: %w", clusterName, err)
	}

	if output.Cluster == nil || output.Cluster.ResourcesVpcConfig == nil {
		return ClusterVPCConfig{}, fmt.Errorf("cluster %s has no VPC config", clusterName)
	}

	vpc := output.Cluster.ResourcesVpcConfig

	clusterSGID := ""
	sgIDs := make([]string, 0, len(vpc.SecurityGroupIds)+1)
	sgIDs = append(sgIDs, vpc.SecurityGroupIds...)
	if vpc.ClusterSecurityGroupId != nil && *vpc.ClusterSecurityGroupId != "" {
		clusterSGID = *vpc.ClusterSecurityGroupId
		sgIDs = append(sgIDs, clusterSGID)
	}

	vpcID := ""
	if vpc.VpcId != nil {
		vpcID = *vpc.VpcId
	}

	return ClusterVPCConfig{
		SubnetIDs:              vpc.SubnetIds,
		SecurityGroupIDs:       sgIDs,
		VpcID:                  vpcID,
		ClusterSecurityGroupID: clusterSGID,
	}, nil
}

// AccessEntryData bundles the live EKS access-entry state for a cluster: the set
// of existing access-entry principal ARNs and, for each, the set of associated
// access-policy ARNs. It mirrors the boto3 list_access_entries /
// list_associated_access_policies probes in
// python-pulumi/src/ptd/pulumi_resources/aws_eks_cluster.py with_eks_access_entries,
// which drive the import-vs-create decisions for each Pulumi AccessEntry /
// AccessPolicyAssociation. Pre-fetched in the step layer and passed to the builder
// (see the migration playbook "Pre-fetch vs Pulumi data sources" — these are
// account-state probes, not Pulumi-managed resources).
type AccessEntryData struct {
	// Entries is the set of principal ARNs that already have an access entry.
	Entries map[string]bool
	// AssociatedPolicies maps a principal ARN to the set of access-policy ARNs
	// already associated with it.
	AssociatedPolicies map[string]map[string]bool
}

// GetAccessEntryData lists the cluster's existing access entries and the access
// policies associated with each. Returns empty maps (not an error) when the
// cluster does not exist yet or the lookups fail, matching the Python defensive
// "could not check" behaviour (it proceeds and lets AWS error if needed).
func GetAccessEntryData(ctx context.Context, c *Credentials, region, clusterName string) (AccessEntryData, error) {
	data := AccessEntryData{
		Entries:            map[string]bool{},
		AssociatedPolicies: map[string]map[string]bool{},
	}
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	var nextToken *string
	for {
		out, err := client.ListAccessEntries(ctx, &eks.ListAccessEntriesInput{
			ClusterName: aws.String(clusterName),
			NextToken:   nextToken,
		})
		if err != nil {
			// Match Python: log-and-ignore; proceed with whatever we have.
			return data, nil
		}
		for _, arn := range out.AccessEntries {
			data.Entries[arn] = true
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	for principalARN := range data.Entries {
		policies := map[string]bool{}
		var pToken *string
		for {
			out, err := client.ListAssociatedAccessPolicies(ctx, &eks.ListAssociatedAccessPoliciesInput{
				ClusterName:  aws.String(clusterName),
				PrincipalArn: aws.String(principalARN),
				NextToken:    pToken,
			})
			if err != nil {
				break
			}
			for _, p := range out.AssociatedAccessPolicies {
				if p.PolicyArn != nil {
					policies[*p.PolicyArn] = true
				}
			}
			if out.NextToken == nil {
				break
			}
			pToken = out.NextToken
		}
		data.AssociatedPolicies[principalARN] = policies
	}

	return data, nil
}

// GetNodeGroupNames returns the managed node group names for an EKS cluster.
func GetNodeGroupNames(ctx context.Context, c *Credentials, region string, clusterName string) ([]string, error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: aws.String(clusterName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodegroups for cluster %s: %w", clusterName, err)
	}

	return output.Nodegroups, nil
}

// addExpiresQueryParam returns a middleware function that adds X-Amz-Expires to the
// presigned URL. EKS requires this parameter for token validation.
func addExpiresQueryParam(seconds int) func(stack *middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("AddExpiresQueryParam",
			func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
				req, ok := in.Request.(*smithyhttp.Request)
				if !ok {
					return middleware.BuildOutput{}, middleware.Metadata{}, fmt.Errorf("unexpected request type %T", in.Request)
				}
				q := req.URL.Query()
				q.Set("X-Amz-Expires", url.QueryEscape(fmt.Sprintf("%d", seconds)))
				req.URL.RawQuery = q.Encode()
				return next.HandleBuild(ctx, in)
			},
		), middleware.After)
	}
}
