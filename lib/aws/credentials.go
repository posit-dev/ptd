package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rstudio/ptd/lib/types"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
)

var positEmail = regexp.MustCompile(`[^:]*@posit\.co$`)

const (
	ProfileEnvVar         = "AWS_PROFILE"
	AccessKeyIdEnvVar     = "AWS_ACCESS_KEY_ID"
	SecretAccessKeyEnvVar = "AWS_SECRET_ACCESS_KEY"
	SessionTokenEnvVar    = "AWS_SESSION_TOKEN"
)

type Credentials struct {
	accountID           string
	assumedRoleUser     string
	roleArn             string
	profile             string
	customRoleArn       string
	externalID          string
	credentialsProvider credentials.StaticCredentialsProvider
}

func (c *Credentials) Expired() bool {
	return c.credentialsProvider.Value.Expired() || c.credentialsProvider.Value.AccessKeyID == ""
}

func (c *Credentials) Refresh(ctx context.Context) error {
	if c.Expired() {
		slog.Debug("Refreshing credentials", "role_arn", c.roleArn, "account_id", c.accountID)
		return c.assumeRole(ctx)
	}
	return nil
}

func (c *Credentials) EnvVars() map[string]string {
	return map[string]string{
		AccessKeyIdEnvVar:     c.credentialsProvider.Value.AccessKeyID,
		SecretAccessKeyEnvVar: c.credentialsProvider.Value.SecretAccessKey,
		SessionTokenEnvVar:    c.credentialsProvider.Value.SessionToken,
	}
}

func (c *Credentials) AccountID() string {
	return c.accountID
}

func (c *Credentials) Identity() string {
	if c.useProfile() {
		return fmt.Sprintf("profile/%s", c.profile)
	}
	return c.roleArn
}

func (c *Credentials) useProfile() bool {
	if c.profile != "" {
		return true
	}
	return false
}

func NewCredentials(accountID string, profile string, customRoleArn string, externalID string) (c *Credentials) {
	return &Credentials{
		roleArn:       fmt.Sprintf("arn:aws:iam::%s:role/admin.posit.team", accountID),
		accountID:     accountID,
		profile:       profile,
		customRoleArn: customRoleArn,
		externalID:    externalID,
	}
}

// AssumeRole assumes an AWS role and returns the credentials
func (c *Credentials) assumeRole(ctx context.Context) error {
	// we want to support using a profile, but ultimately want to return static credentials
	// the steps for how to do this seem non-existent or maybe impossible in the go sdk.
	// for expediency, we will shell out to the aws cli if a profile is specified
	if c.useProfile() {
		provider, err := getCredentialsProviderForProfileFromCli(ctx, c.profile)
		if err != nil {
			return err
		}

		// If a custom role ARN is specified, use the profile credentials to assume that role
		if c.customRoleArn != "" {
			slog.Debug("Assuming custom role with profile credentials", "profile", c.profile, "role_arn", c.customRoleArn)

			// Create AWS config with the profile credentials
			cfg, err := config.LoadDefaultConfig(ctx,
				config.WithCredentialsProvider(provider),
			)
			if err != nil {
				return fmt.Errorf("unable to load SDK config with profile credentials: %v", err)
			}

			// Create STS client with profile credentials
			svc := sts.NewFromConfig(cfg)

			// Assume the custom role
			input := &sts.AssumeRoleInput{
				RoleArn:         aws.String(c.customRoleArn),
				RoleSessionName: aws.String(sessionName(ctx)),
			}

			// Add external ID if provided
			if c.externalID != "" {
				input.ExternalId = aws.String(c.externalID)
			}

			result, err := svc.AssumeRole(ctx, input)
			if err != nil {
				return fmt.Errorf("unable to assume role %s with profile %s: %w", c.customRoleArn, c.profile, err)
			}

			// Set the assumed role credentials
			c.assumedRoleUser = *result.AssumedRoleUser.Arn
			c.credentialsProvider = credentials.NewStaticCredentialsProvider(
				*result.Credentials.AccessKeyId,
				*result.Credentials.SecretAccessKey,
				*result.Credentials.SessionToken)

			return nil
		}

		// No custom role specified, just use the profile credentials directly
		c.assumedRoleUser = fmt.Sprintf("profile/%s", c.profile) // this might cause a bug somewhere.
		c.credentialsProvider = provider
		return nil
	}

	// Load the default AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("unable to load SDK config, %v", err)
	}

	// Create a new STS client
	svc := sts.NewFromConfig(cfg)

	// Assume the role
	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(c.roleArn),
		RoleSessionName: aws.String(sessionName(ctx)),
	}

	result, err := svc.AssumeRole(ctx, input)
	if err != nil {
		return fmt.Errorf("unable to assume role, %v", err)
	}

	// Return the credentials
	c.assumedRoleUser = *result.AssumedRoleUser.Arn
	c.credentialsProvider = credentials.NewStaticCredentialsProvider(
		*result.Credentials.AccessKeyId,
		*result.Credentials.SecretAccessKey,
		*result.Credentials.SessionToken)

	return nil
}

// GetCallerIdentity returns the caller's identity
func GetCallerIdentity(ctx context.Context) (out *sts.GetCallerIdentityOutput, err error) {
	// Load the default AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config, %v", err)
	}

	// Create a new STS client
	svc := sts.NewFromConfig(cfg)

	// Call GetCallerIdentity to verify credentials
	out, err = svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("unable to verify credentials, %v", err)
	}

	return out, nil
}

// SessionName parses an SSO-based caller identity to return a session name for further role assumption.
func sessionName(ctx context.Context) string {
	var caller string
	i, err := GetCallerIdentity(ctx)
	if err != nil {
		caller = "unknown"
	} else {
		email := positEmail.FindString(*i.UserId)
		caller = strings.Replace(email, "@posit.co", "", 1)
	}
	return fmt.Sprintf("%s@ptd", caller)
}

func OnlyAwsCredentials(c types.Credentials) (*Credentials, error) {
	v, ok := c.(*Credentials)
	if !ok {
		return nil, fmt.Errorf("reached AWS specific package with non-AWS credentials")
	}
	return v, nil
}

func getCredentialsProviderForProfileFromCli(ctx context.Context, profile string) (credentials.StaticCredentialsProvider, error) {
	slog.Debug("Using AWS profile to get credentials", "profile", profile)
	cmd := exec.CommandContext(ctx,
		"aws",
		"configure",
		"export-credentials",
		"--profile", profile,
		"--output", "json")

	out, err := cmd.Output()
	if err != nil {
		return credentials.NewStaticCredentialsProvider("", "", ""),
			fmt.Errorf("unable to get credentials from profile %s: %v", profile, err)
	}

	// cmd stdout is json with AccessKeyId, SecretAccessKey, SessionToken, unmarshal from output
	var creds struct {
		AccessKeyId     string
		SecretAccessKey string
		SessionToken    string
	}
	err = json.Unmarshal(out, &creds)
	if err != nil {
		return credentials.StaticCredentialsProvider{},
			fmt.Errorf("unable to parse credentials from profile %s: %v", profile, err)
	}

	return credentials.NewStaticCredentialsProvider(
		creds.AccessKeyId,
		creds.SecretAccessKey,
		creds.SessionToken), nil

}
