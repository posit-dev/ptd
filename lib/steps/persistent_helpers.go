package steps

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// persistentAWSWorkloadProjectName is the OLD Python Pulumi project name for the
// AWS workload persistent step. Used verbatim in alias URNs (the migration
// playbook forbids ctx.Project() in alias URNs). Phases D (control room) and E
// (azure) will add their own project-name constants.
const persistentAWSWorkloadProjectName = "ptd-aws-workload-persistent"

// persistentAWSWorkloadCompType is the Python ComponentResource type token for
// the AWS workload persistent step (single super().__init__ — no deep nesting).
const persistentAWSWorkloadCompType = "ptd:AWSWorkloadPersistent"

// persistentAWSVpcOuterCompType is the OuterCompType passed into aws.NewVPC so
// the VPC builder's child resources alias to the old Python URN chain
// ptd:AWSWorkloadPersistent$ptd:AWSVpc$<type>::<name>.
const persistentAWSVpcOuterCompType = persistentAWSWorkloadCompType + "$ptd:AWSVpc"

// persistentAWSControlRoomProjectName is the OLD Python Pulumi project name for
// the AWS control-room persistent step. Used verbatim in alias URNs.
const persistentAWSControlRoomProjectName = "ptd-aws-control-room-persistent"

// persistentAWSControlRoomCompType is the Python ComponentResource type token
// for the AWS control-room persistent step.
const persistentAWSControlRoomCompType = "ptd:AWSControlRoomPersistent"

// persistentAWSControlRoomVpcOuterCompType is the OuterCompType passed into
// aws.NewVPC for the control room so the VPC builder's children alias to the old
// Python URN chain ptd:AWSControlRoomPersistent$ptd:AWSVpc$<type>::<name>.
const persistentAWSControlRoomVpcOuterCompType = persistentAWSControlRoomCompType + "$ptd:AWSVpc"

// persistentControlRoomManagedByValue is the posit.team/managed-by tag value
// Python set on AWS control-room persistent resources (the Python module
// __name__).
const persistentControlRoomManagedByValue = "ptd.pulumi_resources.aws_control_room_persistent"

// controlRoomVPCEndpointServices mirrors the hardcoded endpoint set in
// aws_control_room_persistent.py _define_vpc.
var controlRoomVPCEndpointServices = []string{
	"ec2", "ec2messages", "kms", "s3", "ssm", "ssmmessages",
}

// standardVPCEndpointServices mirrors python-pulumi/src/ptd/aws_workload.py
// STANDARD_VPC_ENDPOINT_SERVICES (note: "fsx" is intentionally absent — the
// persistent step adds the fsx endpoint separately).
var standardVPCEndpointServices = []string{
	"ec2", "ec2messages", "kms", "s3", "ssm", "ssmmessages",
}

// lbcPolicyJSON is the AWS Load Balancer Controller IAM policy document, copied
// verbatim from python-pulumi/src/ptd/iam/aws-load-balancer-controller.policy.json
// (the Python code read it from that file). The exact JSON is load-bearing for
// state parity, so it is embedded rather than referenced by path.
//
//go:embed aws-load-balancer-controller.policy.json
var lbcPolicyJSON string

// teamOperatorPolicyJSON is the team-operator IAM policy document. Python built
// it via aws.iam.get_policy_document with a single statement allowing
// secretsmanager:GetSecretValue on "*".
func teamOperatorPolicyJSON() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":   "Allow",
				"Action":   "secretsmanager:GetSecretValue",
				"Resource": "*",
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// alloyPolicyJSON mirrors the Alloy IAM policy from _define_alloy_iam.
func alloyPolicyJSON() string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"tag:GetResources",
					"cloudwatch:GetMetricData",
					"cloudwatch:GetMetricStatistics",
					"cloudwatch:ListMetrics",
				},
				"Resource": []string{"*"},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// traefikForwardAuthSecretsPolicyJSON mirrors ptd.aws_traefik_forward_auth_secrets_policy.
func traefikForwardAuthSecretsPolicyJSON(region, accountID string) string {
	doc := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"secretsmanager:GetSecretValue",
					"secretsmanager:DescribeSecret",
				},
				"Resource": []string{
					fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:okta-oidc-client-creds-*", region, accountID),
					fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:okta-oidc-client-creds.*.posit.team", region, accountID),
					fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:okta-oidc-client-creds.*.posit.team*", region, accountID),
				},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// bucketReadWritePolicyActions mirrors the READ_WRITE action set in
// python-pulumi/src/ptd/pulumi_resources/aws_bucket.py define_bucket_policy.
var bucketReadWritePolicyActions = []string{
	"s3:AbortMultipartUpload",
	"s3:DeleteObject",
	"s3:GetBucketLocation",
	"s3:GetObject",
	"s3:GetObjectTagging",
	"s3:HeadObject",
	"s3:ListBucket",
	"s3:ListObjects",
	"s3:PutObject",
	"s3:PutObjectTagging",
}

// irsaTrustPolicyLogic is the pure JSON-building body for an IRSA assume-role
// policy, mirroring ptd.aws_iam.build_irsa_role_assume_role_policy. When there
// are no OIDC providers, Python falls back to a statement whose Principal.AWS is
// the caller identity ARN (aws.get_caller_identity().arn) — NOT account root — so
// callerARN is threaded in to match state exactly.
//
// It is the shared, Output-free core used by the eks step's Output-aware wrapper
// irsaTrustPolicyOutput (the persistent step no longer creates IRSA roles). The
// logic is unchanged from the original persistentIRSATrustPolicy.
func irsaTrustPolicyLogic(namespace string, serviceAccounts, oidcURLTails []string, accountID, callerARN string) string {
	if len(oidcURLTails) == 0 {
		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Action": "sts:AssumeRole",
					"Effect": "Allow",
					"Principal": map[string]interface{}{
						"AWS": callerARN,
					},
				},
			},
		}
		b, _ := json.Marshal(doc)
		return string(b)
	}

	var statements []map[string]interface{}
	for _, oidcTail := range oidcURLTails {
		subs := make([]string, len(serviceAccounts))
		for i, sa := range serviceAccounts {
			subs[i] = fmt.Sprintf("system:serviceaccount:%s:%s", namespace, sa)
		}
		statements = append(statements, map[string]interface{}{
			"Action": "sts:AssumeRoleWithWebIdentity",
			"Effect": "Allow",
			"Principal": map[string]interface{}{
				"Federated": fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, oidcTail),
			},
			"Condition": map[string]interface{}{
				"StringEquals": map[string]interface{}{
					fmt.Sprintf("%s:aud", oidcTail): "sts.amazonaws.com",
					fmt.Sprintf("%s:sub", oidcTail): subs,
				},
			},
		})
	}
	doc := map[string]interface{}{
		"Version":   "2012-10-17",
		"Statement": statements,
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

// irsaTrustPolicyOutput is the Output-aware wrapper around irsaTrustPolicyLogic
// used by the eks step. It takes the cluster OIDC issuer URLs as Pulumi Outputs
// (e.g. eksCluster.OidcProvider().Url, one per cluster), strips the "https://"/
// "http://" scheme prefix to get the OIDC tail, sorts the tails (matching the
// persistent step's deterministic ordering), and builds the assume-role policy
// JSON via the shared pure logic. callerARN is the fallback Principal.AWS used
// only when no OIDC URLs are supplied (it should not be hit in the eks step,
// where the cluster always exists, but is threaded through for parity).
//
// The signature is list-shaped over OIDC URLs so a multi-cluster workload would
// aggregate every issuer into one trust policy; single-cluster is the norm.
func irsaTrustPolicyOutput(oidcURLs []pulumi.StringOutput, namespace string, serviceAccounts []string, accountID, callerARN string) pulumi.StringOutput {
	inputs := make([]interface{}, len(oidcURLs))
	for i, u := range oidcURLs {
		inputs[i] = u
	}
	return pulumi.All(inputs...).ApplyT(func(args []interface{}) string {
		var tails []string
		for _, a := range args {
			u, _ := a.(string)
			tail := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
			if tail != "" {
				tails = append(tails, tail)
			}
		}
		sort.Strings(tails)
		return irsaTrustPolicyLogic(namespace, serviceAccounts, tails, accountID, callerARN)
	}).(pulumi.StringOutput)
}

// awsTagMap converts a string→string tag map to a pulumi.StringMap, merging an
// optional "Name" tag.
func awsTagMap(tags map[string]string, extras map[string]string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, v := range tags {
		out[k] = pulumi.String(v)
	}
	for k, v := range extras {
		out[k] = pulumi.String(v)
	}
	return out
}

// boolPtrOrDefault returns *p when p is non-nil, else def.
func boolPtrOrDefault(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// jsonMarshal is a thin wrapper returning (string, error) suitable for use inside
// pulumi ApplyT callbacks.
func jsonMarshal(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
