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

// ListManagedEKSClusterOIDCURLs lists EKS clusters in the account whose name
// contains compoundName and returns their OIDC issuer URLs.
//
// It mirrors the OIDC-discovery half of Python's AWSWorkload.managed_clusters +
// get_oidc_url (python-pulumi/src/ptd/__init__.py aws_eks_clusters): Python lists
// eks:cluster resources carrying the posit.team/managed-by tag, filters by
// compound_name substring, and reads each cluster's identity.oidc.issuer. PTD
// clusters are always named with the compound name (default_<compound>-<release>-
// control-plane), so the substring filter on ListClusters captures the same set
// without requiring the resource-groups-tagging API. Used by the persistent step
// to build IRSA trust policies; returns an empty slice on a greenfield account
// (no clusters yet), matching Python.
func ListManagedEKSClusterOIDCURLs(ctx context.Context, c *Credentials, region, compoundName string) ([]string, error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	var oidcURLs []string
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &eks.ListClustersInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("list EKS clusters: %w", err)
		}
		for _, name := range out.Clusters {
			if !strings.Contains(name, compoundName) {
				continue
			}
			desc, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
			if err != nil {
				// Match Python: log-and-continue on describe failure.
				continue
			}
			if desc.Cluster != nil && desc.Cluster.Identity != nil &&
				desc.Cluster.Identity.Oidc != nil && desc.Cluster.Identity.Oidc.Issuer != nil {
				if issuer := *desc.Cluster.Identity.Oidc.Issuer; issuer != "" {
					oidcURLs = append(oidcURLs, issuer)
				}
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return oidcURLs, nil
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

	sgIDs := make([]string, 0, len(vpc.SecurityGroupIds)+1)
	sgIDs = append(sgIDs, vpc.SecurityGroupIds...)
	if vpc.ClusterSecurityGroupId != nil && *vpc.ClusterSecurityGroupId != "" {
		sgIDs = append(sgIDs, *vpc.ClusterSecurityGroupId)
	}

	vpcID := ""
	if vpc.VpcId != nil {
		vpcID = *vpc.VpcId
	}

	return ClusterVPCConfig{
		SubnetIDs:        vpc.SubnetIds,
		SecurityGroupIDs: sgIDs,
		VpcID:            vpcID,
	}, nil
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
