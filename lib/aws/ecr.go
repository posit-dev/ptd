package aws

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrTypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/rstudio/ptd/lib/helpers"
	"github.com/rstudio/ptd/lib/types"
)

func GetEcrAuthToken(ctx context.Context, c *Credentials, region string) (string, error) {
	client := ecr.New(ecr.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", err
	}

	return helpers.Base64Decode(*output.AuthorizationData[0].AuthorizationToken)
}

func LatestDigestForRepository(ctx context.Context, c *Credentials, region, repository string) (string, error) {
	detail, err := LatestImageForRepository(ctx, c, region, repository)
	if err != nil {
		return "", err
	}

	return detail.Digest, nil
}

func LatestImageForRepository(ctx context.Context, c *Credentials, region, repository string) (detail types.ImageDetails, err error) {
	client := ecr.New(ecr.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	var imageDetails []ecrTypes.ImageDetail
	maxResults := int32(500)
	var nextToken *string
	for {
		output, err := client.DescribeImages(ctx, &ecr.DescribeImagesInput{
			RepositoryName: &repository,
			MaxResults:     &maxResults,
			NextToken:      nextToken,
		})
		if err != nil {
			return detail, err
		}

		imageDetails = append(imageDetails, output.ImageDetails...)

		if output.NextToken == nil {
			break
		}
		nextToken = output.NextToken
	}

	if len(imageDetails) == 0 {
		return
	}

	sort.Slice(imageDetails, func(i, j int) bool {
		return imageDetails[i].ImagePushedAt.After(*imageDetails[j].ImagePushedAt)
	})

	detail = types.ImageDetails{
		Digest: *imageDetails[0].ImageDigest,
		Tags:   imageDetails[0].ImageTags,
	}

	return
}
