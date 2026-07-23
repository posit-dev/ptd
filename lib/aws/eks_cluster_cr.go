package aws

// Control-room-only EKSCluster builder methods. These mirror the with_*() methods
// in python-pulumi/src/ptd/pulumi_resources/aws_eks_cluster.py that are invoked
// exclusively by the control-room wrapper (aws_control_room_cluster.py):
//
//	WithAwsLbc              (~1427)  aws-load-balancer-controller (IRSA + helm)
//	WithMetricsServer       (~1773)  metrics-server helm
//	WithSecretStoreCsi      (~1789)  secrets-store-csi-driver helm
//	WithSecretStoreCsiAwsProvider (~1810) aws provider helm
//	WithTraefikForwardAuth  (~1828)  IRSA + helm + middleware/SecretProviderClass extraObjects
//	WithGrafana             (~1972)  namespace + secrets + alert/dashboard ConfigMaps + helm
//	WithMimir               (~2145)  S3 buckets + IRSA + namespace + helm
//
// They reuse createServiceAccountRole (Phase 3) for IRSA roles, and all helm
// charts are native helm.v3.Release resources (NOT HelmChart CRs) — matching the
// Python control-room which used k8s.helm.v3.Release directly. Values are passed
// as structured pulumi.Map, so yaml.v2 indentation (marshalYAML) does not apply
// here; Pulumi diffs the values structurally exactly as the Python state stored
// them.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	awseks "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/eks"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	awss3 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"golang.org/x/crypto/blowfish"
)

// ── Control-room node group (externally-shaped launch template) ──────────────

// ControlRoomNodeGroupParams holds the control-room node group configuration.
// The control-room launch template differs from the workload one: it is parented
// to the EKS cluster (not the wrapper), omits VolumeSize/instance-metadata-tags/
// VpcSecurityGroupIds, and its tag_specifications carry the bare required_tags.
type ControlRoomNodeGroupParams struct {
	// Name is the node group AND launch-template logical name (the CR compound
	// name). MUST match Python (aws_control_room_cluster.py passes self.name).
	Name         string
	InstanceType string
	AmiType      string
	MinSize      int
	MaxSize      int
	DesiredSize  int
	Version      string
	// Tags is the required_tags map (applied bare to tag_specifications; merged
	// with {"Name": Name} on the LT-level tags and the node-group tags).
	Tags map[string]string
}

// WithControlRoomNodeGroup creates the control-room launch template (parented to
// the cluster) and the managed node group, mirroring
// aws_control_room_cluster.py:_define_eks's inline LaunchTemplate +
// self.eks.with_node_group(...). Requires WithNodeRole.
func (c *EKSCluster) WithControlRoomNodeGroup(p ControlRoomNodeGroupParams) *EKSCluster {
	if c.err != nil {
		return c
	}
	if c.nodeRole == nil {
		c.err = fmt.Errorf("eks: WithControlRoomNodeGroup called before WithNodeRole for %s", c.cfg.Name)
		return c
	}

	bareTags := pulumi.StringMap{}
	for k, v := range p.Tags {
		bareTags[k] = pulumi.String(v)
	}
	nameTags := pulumi.StringMap{"Name": pulumi.String(p.Name)}
	for k, v := range p.Tags {
		nameTags[k] = pulumi.String(v)
	}

	// LaunchTemplate parented to the EKS cluster in Python (parent=self.eks.eks):
	// old URN type chain <ParentTypeChain>$aws:eks/cluster:Cluster$aws:ec2/launchTemplate:LaunchTemplate.
	lt, err := awsec2.NewLaunchTemplate(c.ctx, p.Name, &awsec2.LaunchTemplateArgs{
		UpdateDefaultVersion: pulumi.Bool(true),
		InstanceType:         pulumi.String(p.InstanceType),
		MetadataOptions: &awsec2.LaunchTemplateMetadataOptionsArgs{
			HttpEndpoint:            pulumi.String("enabled"),
			HttpTokens:              pulumi.String("required"),
			HttpPutResponseHopLimit: pulumi.Int(2),
		},
		BlockDeviceMappings: awsec2.LaunchTemplateBlockDeviceMappingArray{
			awsec2.LaunchTemplateBlockDeviceMappingArgs{
				DeviceName: pulumi.String("/dev/xvda"),
				Ebs:        &awsec2.LaunchTemplateBlockDeviceMappingEbsArgs{VolumeType: pulumi.String("gp3")},
			},
		},
		Tags: nameTags,
		TagSpecifications: awsec2.LaunchTemplateTagSpecificationArray{
			awsec2.LaunchTemplateTagSpecificationArgs{ResourceType: pulumi.String("instance"), Tags: bareTags},
			awsec2.LaunchTemplateTagSpecificationArgs{ResourceType: pulumi.String("volume"), Tags: bareTags},
		},
	}, c.clusterChildAlias("aws:ec2/launchTemplate:LaunchTemplate", p.Name))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create control-room launch template %s: %w", p.Name, err)
		return c
	}

	ng, err := awseks.NewNodeGroup(c.ctx, p.Name, &awseks.NodeGroupArgs{
		ClusterName: c.cluster.Name,
		NodeRoleArn: c.nodeRole.Arn,
		SubnetIds:   pulumi.ToStringArray(c.cfg.SubnetIDs),
		Version:     pulumi.String(p.Version),
		ScalingConfig: &awseks.NodeGroupScalingConfigArgs{
			DesiredSize: pulumi.Int(p.DesiredSize),
			MinSize:     pulumi.Int(p.MinSize),
			MaxSize:     pulumi.Int(p.MaxSize),
		},
		AmiType: pulumi.String(p.AmiType),
		Tags:    nameTags,
		LaunchTemplate: &awseks.NodeGroupLaunchTemplateArgs{
			Id:      lt.ID(),
			Version: lt.LatestVersion.ApplyT(func(v int) string { return fmt.Sprintf("%d", v) }).(pulumi.StringOutput),
		},
		UpdateConfig:       &awseks.NodeGroupUpdateConfigArgs{MaxUnavailable: pulumi.Int(1)},
		ForceUpdateVersion: pulumi.Bool(c.cfg.ForceUpdateVersion),
	}, c.clusterChildAlias("aws:eks/nodeGroup:NodeGroup", p.Name))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create control-room node group %s: %w", p.Name, err)
		return c
	}
	c.nodeGroups[p.Name] = ng

	return c
}

// AttachNodeSSMPolicy attaches AmazonSSMManagedInstanceCore to the default node
// role, mirroring aws_control_room_cluster.py:_define_node_iam. The attachment's
// Python parent was the node role (child of the cluster), so its old URN type
// chain is <ParentTypeChain>$aws:eks/cluster:Cluster$aws:iam/role:Role$aws:iam/rolePolicyAttachment.
func (c *EKSCluster) AttachNodeSSMPolicy() *EKSCluster {
	if c.err != nil {
		return c
	}
	if c.nodeRole == nil {
		c.err = fmt.Errorf("eks: AttachNodeSSMPolicy called before WithNodeRole for %s", c.cfg.Name)
		return c
	}
	name := c.cfg.Name + "-eks-nodegroup-ssm"
	urn := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:eks/cluster:Cluster$aws:iam/role:Role$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, name)
	if _, err := awsiam.NewRolePolicyAttachment(c.ctx, name, &awsiam.RolePolicyAttachmentArgs{
		Role:      c.nodeRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	}, pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})); err != nil {
		c.err = fmt.Errorf("eks: failed to attach node SSM policy for %s: %w", c.cfg.Name, err)
		return c
	}
	return c
}

// rolePolicyAlias returns a full-URN alias for an aws.iam.Policy that was a child
// of an IAM role (parent=sa_role). Old URN type chain:
// <ParentTypeChain>$aws:iam/role:Role$aws:iam/policy:Policy.
func (c *EKSCluster) rolePolicyAlias(name string) pulumi.ResourceOption {
	urn := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, name)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// oidcIssuerTail returns the OIDC issuer URL with the leading scheme ("https://")
// stripped, matching Python's url.split("//")[1].
func (c *EKSCluster) oidcIssuerTail() pulumi.StringOutput {
	issuerURL := c.cluster.Identities.Index(pulumi.Int(0)).Oidcs().Index(pulumi.Int(0)).Issuer().Elem()
	return issuerURL.ApplyT(func(url string) (string, error) {
		parts := strings.SplitN(url, "//", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("eks: malformed OIDC issuer URL %q", url)
		}
		return parts[1], nil
	}).(pulumi.StringOutput)
}

// irsaTrustPolicyForSA builds an IRSA assume-role-policy JSON for a single
// service account subject, derived from the cluster's OIDC issuer. Mirrors the
// inline get_policy_document(...) calls in with_traefik_forward_auth / with_mimir.
func (c *EKSCluster) irsaTrustPolicyForSA(subject string) pulumi.StringOutput {
	accountID := c.cfg.AccountID
	return c.oidcIssuerTail().ApplyT(func(tail string) (string, error) {
		doc := map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Effect": "Allow",
					"Principal": map[string]interface{}{
						"Federated": fmt.Sprintf("arn:aws:iam::%s:oidc-provider/%s", accountID, tail),
					},
					"Action": "sts:AssumeRoleWithWebIdentity",
					"Condition": map[string]interface{}{
						"StringEquals": map[string]interface{}{
							fmt.Sprintf("%s:aud", tail): "sts.amazonaws.com",
							fmt.Sprintf("%s:sub", tail): subject,
						},
					},
				},
			},
		}
		b, err := json.Marshal(doc)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}).(pulumi.StringOutput)
}

// ── aws-load-balancer-controller ────────────────────────────────────────────

// WithAwsLbc installs the aws-load-balancer-controller: an IRSA role
// (aws-load-balancer-controller SA in kube-system), the controller IAM policy +
// attachment, the ServiceAccount, and the helm release. Mirrors with_aws_lbc.
// version is the controller image tag override; chartVersion is the helm chart
// version. Both are optional: an empty version leaves image.tag unset so the
// chart supplies its own (correct) default image tag, and an empty chartVersion
// installs the latest chart. Python called with_aws_lbc() with no arguments, so
// both were None (image.tag omitted, latest chart); passing a non-empty value
// here would pin image.tag, which must be a real image tag (e.g. "v2.13.0"),
// NOT a chart version.
func (c *EKSCluster) WithAwsLbc(version, chartVersion string) *EKSCluster {
	if c.err != nil {
		return c
	}

	roleName := c.cfg.Name + "-aws-eks-lbc"
	saRole, err := c.createServiceAccountRole(
		roleName,
		[]serviceAccount{{name: "aws-load-balancer-controller", namespace: "kube-system"}},
		"", "",
	)
	if err != nil {
		c.err = err
		return c
	}

	policyName := c.cfg.Name + "-AWSLoadBalancerControllerIAMPolicy"
	policy, err := awsiam.NewPolicy(c.ctx, c.cfg.Name+"-aws-lbc", &awsiam.PolicyArgs{
		Name:   pulumi.String(policyName),
		Policy: pulumi.String(awsLbcPolicyJSON),
	}, c.rolePolicyAlias(c.cfg.Name+"-aws-lbc"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create aws-lbc policy for %s: %w", c.cfg.Name, err)
		return c
	}

	// Python parents this attachment on the aws_lbc_iam_policy (parent=policy), NOT
	// the role, so its old URN type chain is
	// …$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment. State
	// URN: …$Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::<name>-aws-lbc.
	if _, err = awsiam.NewRolePolicyAttachment(c.ctx, c.cfg.Name+"-aws-lbc", &awsiam.RolePolicyAttachmentArgs{
		PolicyArn: policy.Arn,
		Role:      saRole.Name,
	}, c.roleChildPolicyChildAttachAlias(c.cfg.Name+"-aws-lbc", c.cfg.Name+"-aws-lbc")); err != nil {
		c.err = fmt.Errorf("eks: failed to attach aws-lbc policy for %s: %w", c.cfg.Name, err)
		return c
	}

	if _, err = corev1.NewServiceAccount(c.ctx, c.cfg.Name+"-aws-lbc", &corev1.ServiceAccountArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("aws-load-balancer-controller"),
			Namespace: pulumi.String("kube-system"),
			Labels: pulumi.StringMap{
				"app.kubernetes.io/component": pulumi.String("controller"),
				"app.kubernetes.io/name":      pulumi.String("aws-load-balancer-controller"),
			},
			Annotations: pulumi.StringMap{"eks.amazonaws.com/role-arn": saRole.Arn},
		},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:core/v1:ServiceAccount", c.cfg.Name+"-aws-lbc")); err != nil {
		c.err = fmt.Errorf("eks: failed to create aws-lbc ServiceAccount for %s: %w", c.cfg.Name, err)
		return c
	}

	values := pulumi.Map{
		"clusterName": pulumi.String(c.cfg.Name),
		"serviceAccount": pulumi.Map{
			"create": pulumi.Bool(false),
			"name":   pulumi.String("aws-load-balancer-controller"),
		},
		"hostNetwork": pulumi.Bool(true),
	}
	// Only pin image.tag when an explicit image tag is provided. Otherwise let the
	// chart default the image; injecting an invalid tag (e.g. a chart version like
	// "1.6.0") yields a non-existent image and ImagePullBackOff.
	if version != "" {
		values["image"] = pulumi.Map{"tag": pulumi.String(version)}
	}
	relArgs := &helmv3.ReleaseArgs{
		Chart:          pulumi.String("aws-load-balancer-controller"),
		Namespace:      pulumi.String("kube-system"),
		Name:           pulumi.String("aws-load-balancer-controller"),
		Timeout:        pulumi.Int(900),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://aws.github.io/eks-charts")},
		Values:         values,
	}
	if chartVersion != "" {
		relArgs.Version = pulumi.String(chartVersion)
	}
	if _, err = helmv3.NewRelease(c.ctx, c.cfg.Name+"-aws-lbc", relArgs,
		c.providerOpt(), c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-aws-lbc")); err != nil {
		c.err = fmt.Errorf("eks: failed to create aws-lbc helm release for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// ── metrics-server / secrets-store CSI ──────────────────────────────────────

// WithMetricsServer installs the metrics-server helm chart. Mirrors with_metrics_server.
func (c *EKSCluster) WithMetricsServer(version string) *EKSCluster {
	if c.err != nil {
		return c
	}
	if _, err := helmv3.NewRelease(c.ctx, c.cfg.Name+"-metrics-server", &helmv3.ReleaseArgs{
		Name:           pulumi.String("metrics-server"),
		Chart:          pulumi.String("metrics-server"),
		Version:        pulumi.String(version),
		Namespace:      pulumi.String("kube-system"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://kubernetes-sigs.github.io/metrics-server/")},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-metrics-server")); err != nil {
		c.err = fmt.Errorf("eks: failed to create metrics-server helm release for %s: %w", c.cfg.Name, err)
		return c
	}
	return c
}

// WithSecretStoreCsi installs the secrets-store-csi-driver helm chart. Mirrors
// with_secret_store_csi.
func (c *EKSCluster) WithSecretStoreCsi(version string) *EKSCluster {
	if c.err != nil {
		return c
	}
	if _, err := helmv3.NewRelease(c.ctx, c.cfg.Name+"-secret-store-csi", &helmv3.ReleaseArgs{
		Name:           pulumi.String("secrets-store-csi-driver"),
		Chart:          pulumi.String("secrets-store-csi-driver"),
		Version:        pulumi.String(version),
		Namespace:      pulumi.String("kube-system"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://kubernetes-sigs.github.io/secrets-store-csi-driver/charts")},
		Values:         pulumi.Map{"syncSecret": pulumi.Map{"enabled": pulumi.Bool(true)}},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-secret-store-csi")); err != nil {
		c.err = fmt.Errorf("eks: failed to create secret-store-csi helm release for %s: %w", c.cfg.Name, err)
		return c
	}
	return c
}

// WithSecretStoreCsiAwsProvider installs the secrets-store-csi-driver-provider-aws
// helm chart. Mirrors with_secret_store_csi_aws_provider.
func (c *EKSCluster) WithSecretStoreCsiAwsProvider(version string) *EKSCluster {
	if c.err != nil {
		return c
	}
	if _, err := helmv3.NewRelease(c.ctx, c.cfg.Name+"-secrets-store-csi-driver-provider-aws", &helmv3.ReleaseArgs{
		Name:           pulumi.String("secrets-store-csi-driver-provider-aws"),
		Chart:          pulumi.String("secrets-store-csi-driver-provider-aws"),
		Version:        pulumi.String(version),
		Namespace:      pulumi.String("kube-system"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://aws.github.io/secrets-store-csi-driver-provider-aws")},
		Values:         pulumi.Map{},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-secrets-store-csi-driver-provider-aws")); err != nil {
		c.err = fmt.Errorf("eks: failed to create secrets-store-csi aws provider helm release for %s: %w", c.cfg.Name, err)
		return c
	}
	return c
}

// ── traefik-forward-auth ─────────────────────────────────────────────────────

const traefikForwardAuthSA = "traefik-forward-auth.posit.team"

// WithTraefikForwardAuth creates the traefik-forward-auth IRSA role + secrets
// policy + helm release (with Middleware/SecretProviderClass extraObjects, pod
// env/volumes, and the sso ingress). Mirrors with_traefik_forward_auth. The
// dependsOn option lets the caller order this after the Traefik release.
func (c *EKSCluster) WithTraefikForwardAuth(domain, version string, dependsOn []pulumi.Resource) *EKSCluster {
	if c.err != nil {
		return c
	}
	accountID := c.cfg.AccountID
	roleName := fmt.Sprintf("traefik-forward-auth.%s.posit.team", c.cfg.Name)

	trust := c.irsaTrustPolicyForSA(fmt.Sprintf("system:serviceaccount:kube-system:%s", traefikForwardAuthSA))
	roleArgs := &awsiam.RoleArgs{Name: pulumi.String(roleName), AssumeRolePolicy: trust}
	if c.cfg.IAMPermissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(c.cfg.IAMPermissionsBoundary)
	}
	role, err := awsiam.NewRole(c.ctx, roleName, roleArgs, c.fullURNAlias("aws:iam/role:Role", roleName))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create traefik-forward-auth role for %s: %w", c.cfg.Name, err)
		return c
	}

	secretsPolicyDoc, _ := json.Marshal(map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{"secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:secretsmanager:*:%s:secret:okta-oidc-client-creds-*", accountID),
					fmt.Sprintf("arn:aws:secretsmanager:*:%s:secret:okta-oidc-client-creds.*.posit.team", accountID),
					fmt.Sprintf("arn:aws:secretsmanager:*:%s:secret:okta-oidc-client-creds.*.posit.team*", accountID),
				},
			},
		},
	})
	policyName := c.cfg.Name + "-traefik-forward-auth-secrets-policy"
	policy, err := awsiam.NewPolicy(c.ctx, policyName, &awsiam.PolicyArgs{
		Name:   pulumi.String(policyName),
		Policy: pulumi.String(string(secretsPolicyDoc)),
	}, c.rolePolicyAlias(policyName))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create traefik-forward-auth policy for %s: %w", c.cfg.Name, err)
		return c
	}

	if _, err = awsiam.NewRolePolicyAttachment(c.ctx, c.cfg.Name+"-traefik-forward-auth", &awsiam.RolePolicyAttachmentArgs{
		PolicyArn: policy.Arn,
		Role:      role.Name,
	}, c.roleChildPolicyChildAttachAlias(policyName, c.cfg.Name+"-traefik-forward-auth")); err != nil {
		c.err = fmt.Errorf("eks: failed to attach traefik-forward-auth policy for %s: %w", c.cfg.Name, err)
		return c
	}

	objectsJSON, _ := json.Marshal([]map[string]interface{}{
		{
			"jmesPath": []map[string]interface{}{
				{"objectAlias": "clientId", "path": "oidcClientId"},
				{"objectAlias": "clientSecret", "path": "oidcClientSecret"},
				{"objectAlias": "signingSecret", "path": "signingSecret"},
			},
			"objectName": "okta-oidc-client-creds",
			"objectType": "secretsmanager",
		},
	})

	values := pulumi.Map{
		"config": pulumi.Map{
			"auth-host":                 pulumi.String("sso." + domain),
			"cookie-domain":             pulumi.String(domain),
			"cookie-name":               pulumi.String("ptd_mgmt_auth"),
			"csrf-cookie-name":          pulumi.String("csrf_ptd_mgmt_auth"),
			"default-provider":          pulumi.String("oidc"),
			"log-level":                 pulumi.String("debug"),
			"providers.oidc.issuer-url": pulumi.String("https://posit.okta.com"),
			"url-path":                  pulumi.String("/__oauth__"),
		},
		"serviceAccount": pulumi.Map{
			"create": pulumi.Bool(true),
			"name":   pulumi.String(traefikForwardAuthSA),
			"annotations": pulumi.Map{
				"eks.amazonaws.com/role-arn": pulumi.String(fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)),
			},
		},
		"extraObjects": traefikAuthExtraObjects(string(objectsJSON)),
		"pod": pulumi.Map{
			"env":          traefikAuthPodEnv(),
			"volumes":      traefikAuthVolumes(),
			"volumeMounts": traefikAuthVolumeMounts(),
		},
		"ingress": pulumi.Map{
			"enabled":   pulumi.Bool(true),
			"className": pulumi.String("traefik"),
			"annotations": pulumi.Map{
				"traefik.ingress.kubernetes.io/router.middlewares": pulumi.String("kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth@kubernetescrd"),
			},
			"hosts": pulumi.Array{
				pulumi.Map{
					"host":  pulumi.String("sso." + domain),
					"paths": pulumi.Array{pulumi.String("/")},
				},
			},
		},
	}

	opts := []pulumi.ResourceOption{
		c.providerOpt(),
		pulumi.DeleteBeforeReplace(true),
		c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-traefik-forward-auth"),
	}
	if len(dependsOn) > 0 {
		opts = append(opts, pulumi.DependsOn(dependsOn))
	}
	if _, err = helmv3.NewRelease(c.ctx, c.cfg.Name+"-traefik-forward-auth", &helmv3.ReleaseArgs{
		Name:           pulumi.String("traefik-forward-auth"),
		Chart:          pulumi.String("traefik-forward-auth"),
		Version:        pulumi.String(version),
		Namespace:      pulumi.String("kube-system"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://colearendt.github.io/helm")},
		Values:         values,
	}, opts...); err != nil {
		c.err = fmt.Errorf("eks: failed to create traefik-forward-auth helm release for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// roleChildPolicyChildAttachAlias returns a full-URN alias for a
// RolePolicyAttachment whose Python parent was an aws.iam.Policy that was itself a
// child of an aws.iam.Role. Old URN type chain:
// <ParentTypeChain>$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment.
func (c *EKSCluster) roleChildPolicyChildAttachAlias(policyName, attachName string) pulumi.ResourceOption {
	urn := fmt.Sprintf(
		"urn:pulumi:%s::%s::%s$aws:iam/role:Role$aws:iam/policy:Policy$aws:iam/rolePolicyAttachment:RolePolicyAttachment::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, attachName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// traefikAuthExtraObjects mirrors define_traefik_auth_extra_objects.
func traefikAuthExtraObjects(objectsJSON string) pulumi.Array {
	return pulumi.Array{
		pulumi.Map{
			"apiVersion": pulumi.String("traefik.io/v1alpha1"),
			"kind":       pulumi.String("Middleware"),
			"metadata":   pulumi.Map{"name": pulumi.String("traefik-forward-auth"), "namespace": pulumi.String("kube-system")},
			"spec": pulumi.Map{
				"forwardAuth": pulumi.Map{
					"address":             pulumi.String("http://traefik-forward-auth.kube-system.svc.cluster.local"),
					"trustForwardHeader":  pulumi.Bool(true),
					"authResponseHeaders": pulumi.Array{pulumi.String("X-Forwarded-User")},
				},
			},
		},
		pulumi.Map{
			"apiVersion": pulumi.String("traefik.io/v1alpha1"),
			"kind":       pulumi.String("Middleware"),
			"metadata":   pulumi.Map{"name": pulumi.String("traefik-forward-auth-add-forwarded-headers"), "namespace": pulumi.String("kube-system")},
			"spec": pulumi.Map{
				"headers": pulumi.Map{
					"customRequestHeaders": pulumi.Map{
						"X-Forwarded-Proto": pulumi.String("https"),
						"X-Forwarded-Port":  pulumi.String("443"),
					},
				},
			},
		},
		pulumi.Map{
			"apiVersion": pulumi.String("secrets-store.csi.x-k8s.io/v1"),
			"kind":       pulumi.String("SecretProviderClass"),
			"metadata":   pulumi.Map{"name": pulumi.String("traefik-forward-auth-oidc-client-creds"), "namespace": pulumi.String("kube-system")},
			"spec": pulumi.Map{
				"provider":   pulumi.String("aws"),
				"parameters": pulumi.Map{"objects": pulumi.String(objectsJSON)},
				"secretObjects": pulumi.Array{
					pulumi.Map{
						"secretName": pulumi.String("traefik-forward-auth-oidc-client-creds"),
						"type":       pulumi.String("Opaque"),
						"data": pulumi.Array{
							pulumi.Map{"key": pulumi.String("clientId"), "objectName": pulumi.String("clientId")},
							pulumi.Map{"key": pulumi.String("clientSecret"), "objectName": pulumi.String("clientSecret")},
							pulumi.Map{"key": pulumi.String("signingSecret"), "objectName": pulumi.String("signingSecret")},
						},
					},
				},
			},
		},
	}
}

func traefikAuthPodEnv() pulumi.Array {
	mk := func(name, key string) pulumi.Map {
		return pulumi.Map{
			"name": pulumi.String(name),
			"valueFrom": pulumi.Map{
				"secretKeyRef": pulumi.Map{
					"name": pulumi.String("traefik-forward-auth-oidc-client-creds"),
					"key":  pulumi.String(key),
				},
			},
		}
	}
	return pulumi.Array{
		mk("PROVIDERS_OIDC_CLIENT_ID", "clientId"),
		mk("PROVIDERS_OIDC_CLIENT_SECRET", "clientSecret"),
		mk("SECRET", "signingSecret"),
	}
}

func traefikAuthVolumes() pulumi.Array {
	return pulumi.Array{
		pulumi.Map{
			"name": pulumi.String("oidc-client-creds"),
			"csi": pulumi.Map{
				"driver":           pulumi.String("secrets-store.csi.k8s.io"),
				"readOnly":         pulumi.Bool(true),
				"volumeAttributes": pulumi.Map{"secretProviderClass": pulumi.String("traefik-forward-auth-oidc-client-creds")},
			},
		},
	}
}

func traefikAuthVolumeMounts() pulumi.Array {
	return pulumi.Array{
		pulumi.Map{
			"name":      pulumi.String("oidc-client-creds"),
			"mountPath": pulumi.String("/mnt/secrets/oidc-client-creds"),
			"readOnly":  pulumi.Bool(true),
		},
	}
}

// ── Grafana ──────────────────────────────────────────────────────────────────

// GrafanaConfigMapFile is a pre-read alert or dashboard file passed to WithGrafana.
type GrafanaConfigMapFile struct {
	// LogicalSuffix is the resource-name/metadata suffix (alert: stem with "_"→"-";
	// dashboard: sanitized stem).
	LogicalSuffix string
	// DataKey is the ConfigMap data key ("alerts.yaml" for alerts; "<stem>.json"
	// for dashboards).
	DataKey string
	// Content is the file contents (raw YAML for alerts; UID/id-normalized JSON for
	// dashboards).
	Content string
}

// GrafanaParams bundles the inputs for WithGrafana.
type GrafanaParams struct {
	Domain string
	// DBConnectionURL is the grafana postgres connection string
	// (db_grafana_connection from the control-room postgres_config stack).
	DBConnectionURL string
	OpsgenieKey     string
	// WLAccountIDs is the sorted list of workload AWS account ids + Azure tenant ids
	// (the X-Scope-OrgID multi-tenant header value, "|"-joined).
	WLAccountIDs []string
	Version      string
	Alerts       []GrafanaConfigMapFile
	Dashboards   []GrafanaConfigMapFile
}

// WithGrafana creates the grafana namespace, the opsgenie secret, the alert and
// dashboard ConfigMaps, and the grafana helm release. Mirrors with_grafana +
// _create_alert_configmaps + _create_dashboard_configmaps.
func (c *EKSCluster) WithGrafana(p GrafanaParams) *EKSCluster {
	if c.err != nil {
		return c
	}

	ns, err := corev1.NewNamespace(c.ctx, c.cfg.Name+"-grafana-ns", &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{Name: pulumi.String("grafana")},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:core/v1:Namespace", c.cfg.Name+"-grafana-ns"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create grafana namespace for %s: %w", c.cfg.Name, err)
		return c
	}

	if _, err = corev1.NewSecret(c.ctx, c.cfg.Name+"-opsgenie-secret", &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{Name: pulumi.String("opsgenie-api-key"), Namespace: pulumi.String("grafana")},
		Data: pulumi.StringMap{
			"POSIT_OPSGENIE_KEY": pulumi.String(base64.StdEncoding.EncodeToString([]byte(p.OpsgenieKey))),
		},
	}, c.providerOpt(), pulumi.DependsOn([]pulumi.Resource{ns}),
		c.fullURNAlias("kubernetes:core/v1:Secret", c.cfg.Name+"-opsgenie-secret")); err != nil {
		c.err = fmt.Errorf("eks: failed to create opsgenie secret for %s: %w", c.cfg.Name, err)
		return c
	}

	// Alert ConfigMaps (sorted by caller). label grafana_alert=1.
	for _, f := range p.Alerts {
		cmName := fmt.Sprintf("%s-grafana-%s-alerts", c.cfg.Name, f.LogicalSuffix)
		metaName := fmt.Sprintf("grafana-%s-alerts", f.LogicalSuffix)
		if _, err = corev1.NewConfigMap(c.ctx, cmName, &corev1.ConfigMapArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(metaName),
				Namespace: pulumi.String("grafana"),
				Labels:    pulumi.StringMap{"grafana_alert": pulumi.String("1")},
			},
			Data: pulumi.StringMap{f.DataKey: pulumi.String(f.Content)},
		}, c.providerOpt(), pulumi.DependsOn([]pulumi.Resource{ns}),
			c.fullURNAlias("kubernetes:core/v1:ConfigMap", cmName)); err != nil {
			c.err = fmt.Errorf("eks: failed to create grafana alert configmap %s: %w", cmName, err)
			return c
		}
	}

	// Dashboard ConfigMaps. label grafana_dashboard=1.
	for _, f := range p.Dashboards {
		cmName := fmt.Sprintf("%s-grafana-%s-dashboard", c.cfg.Name, f.LogicalSuffix)
		metaName := fmt.Sprintf("grafana-%s-dashboard", f.LogicalSuffix)
		if _, err = corev1.NewConfigMap(c.ctx, cmName, &corev1.ConfigMapArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(metaName),
				Namespace: pulumi.String("grafana"),
				Labels:    pulumi.StringMap{"grafana_dashboard": pulumi.String("1")},
			},
			Data: pulumi.StringMap{f.DataKey: pulumi.String(f.Content)},
		}, c.providerOpt(), pulumi.DependsOn([]pulumi.Resource{ns}),
			c.fullURNAlias("kubernetes:core/v1:ConfigMap", cmName)); err != nil {
			c.err = fmt.Errorf("eks: failed to create grafana dashboard configmap %s: %w", cmName, err)
			return c
		}
	}

	orgID := strings.Join(p.WLAccountIDs, "|")
	values := pulumi.Map{
		"alerting":      grafanaAlertingValues(),
		"datasources":   grafanaDatasourcesValues(orgID),
		"envFromSecret": pulumi.String("opsgenie-api-key"),
		"grafana.ini": pulumi.Map{
			"server": pulumi.Map{
				"domain":              pulumi.String(p.Domain),
				"root_url":            pulumi.String(fmt.Sprintf("https://%s/grafana", p.Domain)),
				"serve_from_sub_path": pulumi.Bool(true),
			},
			"auth.proxy": pulumi.Map{
				"enabled":         pulumi.Bool(true),
				"header_name":     pulumi.String("X-Forwarded-User"),
				"header_property": pulumi.String("username"),
				"auto_sign_up":    pulumi.Bool(true),
			},
			"auth":     pulumi.Map{"disable_signout_menu": pulumi.Bool(true)},
			"database": pulumi.Map{"url": pulumi.String(p.DBConnectionURL), "ssl_mode": pulumi.String("require")},
			"users":    pulumi.Map{"auto_assign_org_role": pulumi.String("Editor")},
		},
		"ingress": pulumi.Map{
			"enabled": pulumi.Bool(true),
			"annotations": pulumi.Map{
				"traefik.ingress.kubernetes.io/router.middlewares": pulumi.String("kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth@kubernetescrd"),
			},
			"hosts": pulumi.Array{pulumi.String(p.Domain)},
			"path":  pulumi.String("/grafana"),
		},
		"sidecar": pulumi.Map{
			"alerts": pulumi.Map{"enabled": pulumi.Bool(true), "searchNamespace": pulumi.String("grafana")},
			"dashboards": pulumi.Map{
				"enabled":         pulumi.Bool(true),
				"searchNamespace": pulumi.String("grafana"),
				"label":           pulumi.String("grafana_dashboard"),
			},
		},
	}

	if _, err = helmv3.NewRelease(c.ctx, c.cfg.Name+"-grafana", &helmv3.ReleaseArgs{
		Name:           pulumi.String("grafana"),
		Chart:          pulumi.String("grafana"),
		Version:        pulumi.String(p.Version),
		Namespace:      pulumi.String("grafana"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://grafana.github.io/helm-charts")},
		Values:         values,
	}, c.providerOpt(), pulumi.DeleteBeforeReplace(true), pulumi.IgnoreChanges([]string{"checksum"}),
		pulumi.DependsOn([]pulumi.Resource{ns}),
		c.fullURNAlias("kubernetes:helm.sh/v3:Release", c.cfg.Name+"-grafana")); err != nil {
		c.err = fmt.Errorf("eks: failed to create grafana helm release for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// grafanaAlertingValues mirrors the with_grafana "alerting" values block. The
// template strings use Grafana's {{ "{{" }} escaping and are reproduced verbatim.
func grafanaAlertingValues() pulumi.Map {
	const descTemplate = `{{ "{{" }} define "ptd.description" {{ "}}" }}{{ "{{" }} range .Alerts {{ "}}" }}{{ "{{" }} .Annotations.description {{ "}}" }}` +
		"\n\nSource: " +
		`{{ "{{" }} .GeneratorURL {{ "}}" }}` +
		"\nSilence: " +
		`{{ "{{" }} .SilenceURL {{ "}}" }}{{ "{{" }} end {{ "}}" }}{{ "{{" }} end {{ "}}" }}`
	return pulumi.Map{
		"templates.yaml": pulumi.Map{
			"apiVersion": pulumi.Int(1),
			"templates": pulumi.Array{
				pulumi.Map{
					"orgId":    pulumi.Int(1),
					"name":     pulumi.String("ptd_templates"),
					"template": pulumi.String(descTemplate),
				},
			},
		},
		"contactpoints.yaml": pulumi.Map{
			"apiVersion": pulumi.Int(1),
			"contactPoints": pulumi.Array{
				pulumi.Map{
					"orgId": pulumi.Int(1),
					"name":  pulumi.String("posit-opsgenie"),
					"receivers": pulumi.Array{
						pulumi.Map{
							"uid":  pulumi.String("posit-opsgenie"),
							"type": pulumi.String("opsgenie"),
							"settings": pulumi.Map{
								"apiKey":      pulumi.String(`${{ "{" }}POSIT_OPSGENIE_KEY{{ "}" }}`),
								"apiUrl":      pulumi.String("https://api.opsgenie.com/v2/alerts"),
								"sendTagsAs":  pulumi.String("tags"),
								"message":     pulumi.String(`{{ "{{" }} .CommonAnnotations.summary {{ "}}" }}`),
								"description": pulumi.String(`{{ "{{" }} template "ptd.description" . {{ "}}" }}`),
							},
						},
					},
				},
			},
		},
		"policies.yaml": pulumi.Map{
			"apiVersion": pulumi.Int(1),
			"policies": pulumi.Array{
				pulumi.Map{
					"orgId":           pulumi.Int(1),
					"receiver":        pulumi.String("posit-opsgenie"),
					"group_by":        pulumi.Array{pulumi.String("alertname"), pulumi.String("cluster"), pulumi.String("ptd_component"), pulumi.String("health_check_url")},
					"matchers":        pulumi.Array{pulumi.String("opsgenie = 1")},
					"group_wait":      pulumi.String("30s"),
					"group_interval":  pulumi.String("5m"),
					"repeat_interval": pulumi.String("4h"),
				},
			},
		},
	}
}

// grafanaDatasourcesValues mirrors the with_grafana "datasources" values block.
func grafanaDatasourcesValues(orgID string) pulumi.Map {
	return pulumi.Map{
		"datasources.yaml": pulumi.Map{
			"apiVersion": pulumi.Int(1),
			"datasources": pulumi.Array{
				pulumi.Map{
					"name":           pulumi.String("Mimir"),
					"uid":            pulumi.String("mimir"),
					"type":           pulumi.String("prometheus"),
					"access":         pulumi.String("proxy"),
					"editable":       pulumi.Bool(false),
					"url":            pulumi.String("http://mimir-gateway.mimir.svc.cluster.local/prometheus"),
					"isDefault":      pulumi.Bool(true),
					"jsonData":       pulumi.Map{"httpHeaderName1": pulumi.String("X-Scope-OrgID")},
					"secureJsonData": pulumi.Map{"httpHeaderValue1": pulumi.String(orgID)},
				},
			},
		},
	}
}

// ── Mimir ──────────────────────────────────────────────────────────────────

// MimirParams bundles the inputs for WithMimir.
type MimirParams struct {
	// BucketPrefix is the ruler-storage bucket prefix (mimir_ruler_storage_bucket_prefix).
	BucketPrefix string
	Domain       string
	// Creds maps mimir basic-auth username→password (from the CR mimir-auth secret).
	Creds map[string]string
	// Salt is the bcrypt salt string (CR secret mimir-password-salt).
	Salt string
	Tags map[string]string
	// Region is used for the s3 endpoint (Python hardcodes us-east-2).
	Region  string
	Version string
}

// WithMimir creates the mimir block + ruler S3 buckets, the mimir IRSA role +
// storage policy, the mimir namespace, and the mimir-distributed helm release
// (with basic-auth Secret + Middleware extraObjects). Mirrors with_mimir.
func (c *EKSCluster) WithMimir(p MimirParams) *EKSCluster {
	if c.err != nil {
		return c
	}

	tags := pulumi.StringMap{}
	for k, v := range p.Tags {
		tags[k] = pulumi.String(v)
	}

	mkBucket := func(logical, prefix string) (*awss3.Bucket, error) {
		opts := []pulumi.ResourceOption{
			c.fullURNAlias("aws:s3/bucket:Bucket", logical),
			pulumi.RetainOnDelete(true),
		}
		if c.cfg.ProtectPersistentResources {
			opts = append(opts, pulumi.Protect(true))
		}
		return awss3.NewBucket(c.ctx, logical, &awss3.BucketArgs{
			BucketPrefix: pulumi.String(prefix),
			Acl:          pulumi.String("private"),
			Tags:         tags,
		}, opts...)
	}

	blockStorage, err := mkBucket(c.cfg.Name+"-mimir-storage", c.cfg.Name+"-mimir-storage-")
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir block storage bucket for %s: %w", c.cfg.Name, err)
		return c
	}
	rulerStorage, err := mkBucket(c.cfg.Name+"-mimir-ruler-storage", p.BucketPrefix)
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir ruler storage bucket for %s: %w", c.cfg.Name, err)
		return c
	}

	accountID := c.cfg.AccountID
	trust := c.irsaTrustPolicyForSA("system:serviceaccount:mimir:mimir")
	roleArgs := &awsiam.RoleArgs{Name: pulumi.String(c.cfg.Name + "-mimir"), AssumeRolePolicy: trust}
	if c.cfg.IAMPermissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(c.cfg.IAMPermissionsBoundary)
	}
	storageRole, err := awsiam.NewRole(c.ctx, c.cfg.Name+"-mimir", roleArgs,
		c.fullURNAlias("aws:iam/role:Role", c.cfg.Name+"-mimir"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir role for %s: %w", c.cfg.Name, err)
		return c
	}

	storagePolicyDoc, _ := json.Marshal(map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Action": []string{
					"s3:PutObject", "s3:GetBucketLocation", "s3:GetObject", "s3:HeadObject",
					"s3:ListBucket", "s3:ListObjects", "s3:DeleteObject", "s3:GetObjectTagging", "s3:PutObjectTagging",
				},
				"Resource": []string{
					fmt.Sprintf("arn:aws:s3:::%s-mimir-storage-*", c.cfg.Name),
					fmt.Sprintf("arn:aws:s3:::%s*", p.BucketPrefix),
				},
			},
		},
	})
	policyName := c.cfg.Name + "-mimir-storage-policy"
	policy, err := awsiam.NewPolicy(c.ctx, policyName, &awsiam.PolicyArgs{
		Name:   pulumi.String(policyName),
		Policy: pulumi.String(string(storagePolicyDoc)),
	}, c.rolePolicyAlias(policyName))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir storage policy for %s: %w", c.cfg.Name, err)
		return c
	}

	if _, err = awsiam.NewRolePolicyAttachment(c.ctx, c.cfg.Name+"-mimir-storage", &awsiam.RolePolicyAttachmentArgs{
		PolicyArn: policy.Arn,
		Role:      storageRole.Name,
	}, c.roleChildPolicyChildAttachAlias(policyName, c.cfg.Name+"-mimir-storage")); err != nil {
		c.err = fmt.Errorf("eks: failed to attach mimir storage policy for %s: %w", c.cfg.Name, err)
		return c
	}

	ns, err := corev1.NewNamespace(c.ctx, c.cfg.Name+"-mimir-ns", &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{Name: pulumi.String("mimir")},
	}, c.providerOpt(), c.fullURNAlias("kubernetes:core/v1:Namespace", c.cfg.Name+"-mimir-ns"))
	if err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir namespace for %s: %w", c.cfg.Name, err)
		return c
	}

	s3Endpoint := fmt.Sprintf("s3.%s.amazonaws.com", p.Region)

	// bcrypt-hash the basic-auth creds with the provided salt (Python
	// bcrypt.hashpw(pw, salt)). Sorted for deterministic output ordering.
	users := make([]string, 0, len(p.Creds))
	names := make([]string, 0, len(p.Creds))
	for u := range p.Creds {
		names = append(names, u)
	}
	sort.Strings(names)
	for _, u := range names {
		hashed, herr := bcryptHashpw(p.Creds[u], p.Salt)
		if herr != nil {
			c.err = fmt.Errorf("eks: failed to hash mimir cred for %s: %w", u, herr)
			return c
		}
		users = append(users, fmt.Sprintf("%s:%s", u, hashed))
	}
	usersBlock := strings.Join(users, "\n")

	values := pulumi.Map{
		"serviceAccount": pulumi.Map{
			"create": pulumi.Bool(true),
			"name":   pulumi.String("mimir"),
			"annotations": pulumi.Map{
				"eks.amazonaws.com/role-arn": pulumi.String(fmt.Sprintf("arn:aws:iam::%s:role/%s-mimir", accountID, c.cfg.Name)),
			},
		},
		"minio": pulumi.Map{"enabled": pulumi.Bool(false)},
		"mimir": pulumi.Map{
			"structuredConfig": pulumi.Map{
				"blocks_storage": pulumi.Map{
					"backend": pulumi.String("s3"),
					"s3":      pulumi.Map{"bucket_name": blockStorage.Bucket, "endpoint": pulumi.String(s3Endpoint), "insecure": pulumi.Bool(false)},
				},
				"alertmanager_storage": pulumi.Map{
					"backend": pulumi.String("s3"),
					"s3":      pulumi.Map{"bucket_name": rulerStorage.Bucket, "endpoint": pulumi.String(s3Endpoint), "insecure": pulumi.Bool(false)},
				},
				"ruler_storage": pulumi.Map{
					"backend": pulumi.String("s3"),
					"s3":      pulumi.Map{"bucket_name": rulerStorage.Bucket, "endpoint": pulumi.String(s3Endpoint), "insecure": pulumi.Bool(false)},
				},
				"tenant_federation": pulumi.Map{"enabled": pulumi.Bool(true)},
				"limits": pulumi.Map{
					"max_global_series_per_user": pulumi.Int(800000),
					"max_label_names_per_series": pulumi.Int(45),
				},
			},
		},
		"alertmanager": pulumi.Map{"enabled": pulumi.Bool(false)},
		"ingester":     pulumi.Map{"persistentVolume": pulumi.Map{"size": pulumi.String("20Gi")}},
		"compactor":    pulumi.Map{"persistentVolume": pulumi.Map{"size": pulumi.String("20Gi")}},
		"distributor":  pulumi.Map{"replicas": pulumi.Int(3)},
		"store_gateway": pulumi.Map{
			"persistentVolume": pulumi.Map{"size": pulumi.String("20Gi")},
			"replicas":         pulumi.Int(3),
			"resources": pulumi.Map{
				"requests": pulumi.Map{"cpu": pulumi.String("100m"), "memory": pulumi.String("512Mi")},
				"limits":   pulumi.Map{"cpu": pulumi.String("1"), "memory": pulumi.String("4Gi")},
			},
		},
		"nginx": pulumi.Map{"enabled": pulumi.Bool(false)},
		"gateway": pulumi.Map{
			"enabledNonEnterprise": pulumi.Bool(true),
			"ingress": pulumi.Map{
				"enabled": pulumi.Bool(true),
				"annotations": pulumi.Map{
					"traefik.ingress.kubernetes.io/router.middlewares": pulumi.String("mimir-mimir-basic-auth@kubernetescrd"),
				},
				"hosts": pulumi.Array{
					pulumi.Map{
						"host":  pulumi.String(fmt.Sprintf("mimir.%s", p.Domain)),
						"paths": pulumi.Array{pulumi.Map{"path": pulumi.String("/"), "pathType": pulumi.String("Prefix")}},
					},
				},
			},
		},
		"extraObjects": pulumi.Array{
			pulumi.Map{
				"apiVersion": pulumi.String("v1"),
				"kind":       pulumi.String("Secret"),
				"metadata":   pulumi.Map{"name": pulumi.String("mimir-basic-auth"), "namespace": pulumi.String("mimir")},
				"stringData": pulumi.Map{"users": pulumi.String(usersBlock)},
			},
			pulumi.Map{
				"apiVersion": pulumi.String("traefik.io/v1alpha1"),
				"kind":       pulumi.String("Middleware"),
				"metadata":   pulumi.Map{"name": pulumi.String("mimir-basic-auth"), "namespace": pulumi.String("mimir")},
				"spec":       pulumi.Map{"basicAuth": pulumi.Map{"secret": pulumi.String("mimir-basic-auth")}},
			},
		},
	}

	if _, err = helmv3.NewRelease(c.ctx, c.cfg.Name+"-mimir", &helmv3.ReleaseArgs{
		Name:           pulumi.String("mimir"),
		Chart:          pulumi.String("mimir-distributed"),
		Version:        pulumi.String(p.Version),
		Namespace:      pulumi.String("mimir"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://grafana.github.io/helm-charts")},
		Values:         values,
	}, c.providerOpt(), pulumi.DependsOn([]pulumi.Resource{ns}),
		// Python parented the release on the mimir namespace; the namespace was a
		// child of the AWSEKSCluster component, so the release's old URN type chain
		// is <ParentTypeChain>$kubernetes:core/v1:Namespace$kubernetes:helm.sh/v3:Release.
		c.nsChildAlias("kubernetes:helm.sh/v3:Release", "kubernetes:core/v1:Namespace", c.cfg.Name+"-mimir")); err != nil {
		c.err = fmt.Errorf("eks: failed to create mimir helm release for %s: %w", c.cfg.Name, err)
		return c
	}

	return c
}

// nsChildAlias returns a full-URN alias for a resource whose Python parent was a
// Namespace that was itself a child of the AWSEKSCluster component. Old URN type
// chain: <ParentTypeChain>$<nsType>$<resourceType>.
func (c *EKSCluster) nsChildAlias(resourceType, nsType, resourceName string) pulumi.ResourceOption {
	urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s$%s::%s",
		c.ctx.Stack(), c.cfg.ProjectName, c.cfg.ParentTypeChain, nsType, resourceType, resourceName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// ── Deterministic bcrypt (hashpw with an explicit salt) ──────────────────────
//
// Python's bcrypt.hashpw(pw, salt) is DETERMINISTIC for a fixed salt — the
// control-room Pulumi state already stores the resulting hash, so Go must
// reproduce the identical string or the mimir helm release churns every apply.
// Go's stdlib bcrypt.GenerateFromPassword generates a RANDOM salt and exposes no
// hash-with-explicit-salt API, so we reimplement the algorithm directly on top of
// the public golang.org/x/crypto/blowfish package (the same primitive the stdlib
// bcrypt uses internally). This is a faithful port of x/crypto/bcrypt's internal
// bcrypt()/expensiveBlowfishSetup()/Hash() with the salt supplied by the caller.

// bcryptAlphabet is the bcrypt-specific base64 alphabet (NOT standard base64).
const bcryptAlphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var bcryptEncoding = base64.NewEncoding(bcryptAlphabet)

// magicCipherData is the bcrypt IV: "OrpheanBeholderScryDoubt" in bytes.
var magicCipherData = []byte("OrpheanBeholderScryDoubt")

func bcryptBase64Encode(src []byte) []byte {
	n := bcryptEncoding.EncodedLen(len(src))
	dst := make([]byte, n)
	bcryptEncoding.Encode(dst, src)
	for dst[n-1] == '=' {
		n--
	}
	return dst[:n]
}

func bcryptBase64Decode(src []byte) ([]byte, error) {
	numOfEquals := 4 - (len(src) % 4)
	for i := 0; i < numOfEquals; i++ {
		src = append(src, '=')
	}
	dst := make([]byte, bcryptEncoding.DecodedLen(len(src)))
	n, err := bcryptEncoding.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// bcryptHashpw reproduces Python bcrypt.hashpw(password, salt). salt is a full
// modular-crypt salt string of the form "$2b$<cost>$<22-char-base64-salt>"; the
// 60-char modular-crypt hash is returned (matching the Python output stored in
// state).
func bcryptHashpw(password, salt string) (string, error) {
	// Parse "$2<minor>$<cost>$<encodedSalt...>".
	if len(salt) < 7 || salt[0] != '$' {
		return "", fmt.Errorf("malformed bcrypt salt %q", salt)
	}
	parts := strings.SplitN(salt, "$", 4)
	// ["", "2b", "12", "<encodedSalt>(+optional hash)"]
	if len(parts) < 4 {
		return "", fmt.Errorf("malformed bcrypt salt %q", salt)
	}
	version := parts[1] // e.g. "2b"
	var cost int
	if _, err := fmt.Sscanf(parts[2], "%d", &cost); err != nil {
		return "", fmt.Errorf("malformed bcrypt cost in salt %q: %w", salt, err)
	}
	// The encoded salt is the first 22 base64 chars of the trailing segment.
	tail := parts[3]
	if len(tail) < 22 {
		return "", fmt.Errorf("bcrypt salt too short %q", salt)
	}
	encodedSalt := []byte(tail[:22])

	hash, err := bcryptRaw([]byte(password), cost, encodedSalt)
	if err != nil {
		return "", err
	}

	out := fmt.Sprintf("$%s$%02d$%s%s", version, cost, string(encodedSalt), string(hash))
	return out, nil
}

// bcryptRaw is the core bcrypt KDF: encrypt the magic IV 64 times with a
// blowfish cipher set up from the (password, cost, salt). Mirrors
// x/crypto/bcrypt.bcrypt().
func bcryptRaw(password []byte, cost int, encodedSalt []byte) ([]byte, error) {
	cipherData := make([]byte, len(magicCipherData))
	copy(cipherData, magicCipherData)

	c, err := expensiveBlowfishSetup(password, uint32(cost), encodedSalt)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 24; i += 8 {
		for j := 0; j < 64; j++ {
			c.Encrypt(cipherData[i:i+8], cipherData[i:i+8])
		}
	}
	// Bug-compatibility with C bcrypt: only encode 23 of the 24 bytes.
	return bcryptBase64Encode(cipherData[:23]), nil
}

func expensiveBlowfishSetup(key []byte, cost uint32, salt []byte) (*blowfish.Cipher, error) {
	csalt, err := bcryptBase64Decode(salt)
	if err != nil {
		return nil, err
	}
	// C-bcrypt compatibility: trailing NULL in the key during expansion.
	ckey := append(key[:len(key):len(key)], 0)
	c, err := blowfish.NewSaltedCipher(ckey, csalt)
	if err != nil {
		return nil, err
	}
	rounds := uint64(1) << cost
	for i := uint64(0); i < rounds; i++ {
		blowfish.ExpandKey(ckey, c)
		blowfish.ExpandKey(csalt, c)
	}
	return c, nil
}
