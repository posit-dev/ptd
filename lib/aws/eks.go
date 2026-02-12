package aws

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"

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

// GetClusterInfo retrieves the endpoint and certificate authority data for an EKS cluster
func GetClusterInfo(ctx context.Context, c *Credentials, region string, clusterName string) (endpoint string, caCert string, err error) {
	client := eks.New(eks.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(clusterName),
	})
	if err != nil {
		return "", "", err
	}

	// Handle nil pointer dereference if CertificateAuthority is nil
	if output.Cluster == nil || output.Cluster.CertificateAuthority == nil {
		return "", "", nil
	}

	endpoint = ""
	if output.Cluster.Endpoint != nil {
		endpoint = *output.Cluster.Endpoint
	}

	caCert = ""
	if output.Cluster.CertificateAuthority.Data != nil {
		caCert = *output.Cluster.CertificateAuthority.Data
	}

	return endpoint, caCert, nil
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
