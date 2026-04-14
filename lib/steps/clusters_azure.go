package steps

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
	azauthorization "github.com/pulumi/pulumi-azure-native-sdk/authorization/v3"
	azmanagedidentity "github.com/pulumi/pulumi-azure-native-sdk/managedidentity/v3"
	aznetwork "github.com/pulumi/pulumi-azure-native-sdk/network/v3"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	apiextensions "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// --- Azure ---

// azureClustersParams bundles pre-fetched data for the Azure clusters deploy function.
type azureClustersParams struct {
	compoundName                 string
	subscriptionID               string
	region                       string
	resourceGroupName            string
	clusters                     map[string]types.AzureWorkloadClusterConfig
	kubeconfigsByCluster         map[string]string
	dnsForwardDomains            []types.DNSForwardDomainConfig
	resourceTags                 map[string]string
	azureFilesStorageAccountName string
	// clusterIdentityByCluster holds per-cluster identity info (principal IDs, OIDC URL, VNet subnet ID).
	clusterIdentityByCluster map[string]*azure.ClusterIdentityInfo
	// certManagerDomains is the list of domains used for Let's Encrypt CertManager ClusterIssuers.
	// Mirrors Python: root_domain if set, else all site domains.
	certManagerDomains []string
	// thirdPartyTelemetryEnabled controls whether Traefik telemetry arguments are suppressed.
	thirdPartyTelemetryEnabled bool
	// workloadDir is the path to the workload's config directory (contains ptd.yaml, custom_k8s_resources/, etc.)
	workloadDir string
}

func (s *ClustersStep) runAzureInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("clusters: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AzureWorkloadConfig)
	if !ok {
		return fmt.Errorf("clusters: expected AzureWorkloadConfig")
	}

	azTarget, ok := s.DstTarget.(azure.Target)
	if !ok {
		return fmt.Errorf("clusters: expected Azure target")
	}

	azCreds, err := azure.OnlyAzureCredentials(creds)
	if err != nil {
		return err
	}

	// Build per-cluster kubeconfigs and fetch cluster identity info
	kubeconfigsByCluster := make(map[string]string, len(cfg.Clusters))
	clusterIdentityByCluster := make(map[string]*azure.ClusterIdentityInfo, len(cfg.Clusters))
	for release := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		kubeconfigBytes, err := azure.GetKubeCredentials(
			ctx, azCreds, azTarget.SubscriptionID(), azTarget.ResourceGroupName(), clusterName,
		)
		if err != nil {
			return fmt.Errorf("clusters: failed to get AKS kubeconfig for %s: %w", clusterName, err)
		}
		if !s.DstTarget.TailscaleEnabled() {
			kubeconfigBytes, err = kube.AddProxyToKubeConfigBytes(kubeconfigBytes, "socks5://localhost:1080")
			if err != nil {
				return fmt.Errorf("clusters: failed to add proxy to kubeconfig for %s: %w", clusterName, err)
			}
		}
		kubeconfigsByCluster[release] = string(kubeconfigBytes)

		identityInfo, err := azure.GetClusterIdentityInfo(
			ctx, azCreds, azTarget.SubscriptionID(), azTarget.ResourceGroupName(), clusterName,
		)
		if err != nil {
			return fmt.Errorf("clusters: failed to get cluster identity info for %s: %w", clusterName, err)
		}
		clusterIdentityByCluster[release] = identityInfo
	}

	// Build cert manager domains: use root_domain if set, else all site domains.
	// Python: domains = [workload.cfg.root_domain] if workload.cfg.root_domain else workload.cfg.domains
	var certManagerDomains []string
	// AzureWorkloadConfig doesn't expose sites directly in the Go type; derive from Sites map.
	for _, siteCfg := range cfg.Sites {
		certManagerDomains = append(certManagerDomains, siteCfg.Spec.Domain)
	}
	sort.Strings(certManagerDomains)

	// Azure files storage account name: "stptdfiles" + first 14 chars of sanitized compound name.
	// Mirrors python: AzureWorkload.azure_files_storage_account_name
	sanitizedName := strings.ReplaceAll(s.DstTarget.Name(), "-", "")
	if len(sanitizedName) > 14 {
		sanitizedName = sanitizedName[:14]
	}
	azureFilesStorageAccountName := "stptdfiles" + sanitizedName

	params := azureClustersParams{
		compoundName:                 s.DstTarget.Name(),
		subscriptionID:               azTarget.SubscriptionID(),
		region:                       s.DstTarget.Region(),
		resourceGroupName:            azTarget.ResourceGroupName(),
		clusters:                     cfg.Clusters,
		kubeconfigsByCluster:         kubeconfigsByCluster,
		dnsForwardDomains:            cfg.Network.DnsForwardDomains,
		resourceTags:                 cfg.ResourceTags,
		azureFilesStorageAccountName: azureFilesStorageAccountName,
		clusterIdentityByCluster:     clusterIdentityByCluster,
		certManagerDomains:           certManagerDomains,
		thirdPartyTelemetryEnabled:   cfg.ThirdPartyTelemetryEnabled == nil || *cfg.ThirdPartyTelemetryEnabled,
		workloadDir:                  filepath.Join(helpers.GetTargetsConfigPath(), helpers.WorkDir, s.DstTarget.Name()),
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, target types.Target) error {
		return azureClustersDeploy(pctx, target, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

// azureClustersDeploy is the package-level Azure deploy function, callable from tests.
func azureClustersDeploy(ctx *pulumi.Context, _ types.Target, params azureClustersParams) error {
	name := params.compoundName

	// Python component type for alias resolution.
	// All resources were direct children of AzureWorkloadClusters in Python.
	outerComponentType := "ptd:AzureWorkloadClusters"

	// componentURN is the old Python AzureWorkloadClusters component URN.
	componentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s::%s",
		ctx.Stack(), ctx.Project(), outerComponentType, name)

	// withAlias returns an alias pointing to the old Python component parent URN.
	withAlias := func() pulumi.ResourceOption {
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(componentURN)}})
	}

	// withSubComponentAlias returns an alias for resources that were children of a
	// nested Python ComponentResource (e.g., TeamOperator, CertManager, HelmController, etc.).
	withSubComponentAlias := func(subType, subName string) pulumi.ResourceOption {
		parentURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), ctx.Project(), outerComponentType, subType, subName)
		return pulumi.Aliases([]pulumi.Alias{{ParentURN: pulumi.URN(parentURN)}})
	}

	releases := helpers.SortedKeys(params.clusters)

	// azRoleDefID returns a full role definition resource ID for a given role UUID.
	azRoleDefID := func(roleUUID string) string {
		return fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
			params.subscriptionID, roleUUID)
	}

	// rgScope returns the resource group scope used for cluster role assignments.
	rgScope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s",
		params.subscriptionID, params.resourceGroupName)

	for _, release := range releases {
		clusterCfg := params.clusters[release]
		identityInfo := params.clusterIdentityByCluster[release]

		// ── K8s provider ──────────────────────────────────────────────────────
		k8sProviderName := name + "-" + release
		k8sProvider, err := kubernetes.NewProvider(ctx, k8sProviderName, &kubernetes.ProviderArgs{
			Kubeconfig: pulumi.String(params.kubeconfigsByCluster[release]),
		}, withAlias(), pulumi.IgnoreChanges([]string{"kubeconfig"}))
		if err != nil {
			return fmt.Errorf("clusters: failed to create K8s provider for %s: %w", release, err)
		}
		k8sProviderOpt := pulumi.Provider(k8sProvider)

		// ── Role assignments (cluster identity) ───────────────────────────────
		// Python _define_cluster_role_assignments: uses cluster.identity.principalId.
		// Role assignments are direct children of AzureWorkloadClusters.
		if identityInfo != nil && identityInfo.ClusterPrincipalID != "" {
			_, err = azauthorization.NewRoleAssignment(ctx, fmt.Sprintf("%s-aks-reader", release), &azauthorization.RoleAssignmentArgs{
				PrincipalId:      pulumi.String(identityInfo.ClusterPrincipalID),
				PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
				RoleDefinitionId: pulumi.String(azRoleDefID(azRoleReader)),
				Scope:            pulumi.String(rgScope),
			}, withAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create aks-reader role assignment for %s: %w", release, err)
			}

			_, err = azauthorization.NewRoleAssignment(ctx, fmt.Sprintf("%s-aks-network-contributor", release), &azauthorization.RoleAssignmentArgs{
				PrincipalId:      pulumi.String(identityInfo.ClusterPrincipalID),
				PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
				RoleDefinitionId: pulumi.String(azRoleDefID(azRoleNetworkContributor)),
				Scope:            pulumi.String(rgScope),
			}, withAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create aks-network-contributor role assignment for %s: %w", release, err)
			}
		}

		// ── Role assignments (kubelet identity) ───────────────────────────────
		// Python _define_kubelet_role_assignments: uses cluster.identityProfile.kubeletidentity.objectId.
		if identityInfo != nil && identityInfo.KubeletPrincipalID != "" {
			_, err = azauthorization.NewRoleAssignment(ctx, fmt.Sprintf("%s-acrpull", release), &azauthorization.RoleAssignmentArgs{
				PrincipalId:      pulumi.String(identityInfo.KubeletPrincipalID),
				PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
				RoleDefinitionId: pulumi.String(azRoleDefID(azRoleACRPull)),
				Scope:            pulumi.String(rgScope),
			}, withAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create acrpull role assignment for %s: %w", release, err)
			}
		}

		// ── Team Operator ─────────────────────────────────────────────────────
		// Python: TeamOperator is instantiated with compound_name-release as its name.
		teamOpSubName := fmt.Sprintf("%s-%s", name, release)
		withTeamOpAlias := func() pulumi.ResourceOption {
			return withSubComponentAlias("ptd:TeamOperator", teamOpSubName)
		}

		// posit-team namespace (created inside TeamOperator in Python)
		_, err = corev1.NewNamespace(ctx, fmt.Sprintf("%s-%s-%s", name, release, clustersPositTeamNamespace), &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(clustersPositTeamNamespace),
			},
		}, k8sProviderOpt, withTeamOpAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create posit-team namespace for %s: %w", release, err)
		}

		// Team operator Helm release.
		// Python's team_operator.py adds AWS_REGION from workload.cfg.region (set even on Azure),
		// and always includes serviceAccount.annotations (defaulting to {}).
		// We replicate this exactly to avoid a Helm release update on first apply.
		azureTeamOpEnv := pulumi.Map{
			"WATCH_NAMESPACES": pulumi.String(clustersPositTeamNamespace),
		}
		if params.region != "" {
			azureTeamOpEnv["AWS_REGION"] = pulumi.String(params.region)
		}
		_, err = helmv3.NewRelease(ctx, fmt.Sprintf("%s-%s-team-operator", name, release), &helmv3.ReleaseArgs{
			Name:            pulumi.String("team-operator"),
			Chart:           pulumi.String("oci://ghcr.io/posit-dev/charts/team-operator"),
			Version:         pulumi.String(clustersDefaultTeamOperatorChartVersion),
			Namespace:       pulumi.String(clustersPositTeamSystemNamespace),
			CreateNamespace: pulumi.Bool(true),
			Values: pulumi.Map{
				"controllerManager": pulumi.Map{
					"replicas": pulumi.Int(1),
					"container": pulumi.Map{
						"env": azureTeamOpEnv,
					},
					"serviceAccount": pulumi.Map{
						"annotations": pulumi.Map{},
					},
				},
				"crd": pulumi.Map{
					"enable": pulumi.Bool(true),
					"keep":   pulumi.Bool(true),
				},
			},
		}, k8sProviderOpt, withTeamOpAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create team operator for %s: %w", release, err)
		}

		// ── HelmController ─────────────────────────────────────────────────────
		// Python: HelmController component name is "{compound_name}-{release}-helm-controller".
		helmCtrlSubName := fmt.Sprintf("%s-%s-helm-controller", name, release)
		withHelmCtrlAlias := func() pulumi.ResourceOption {
			return withSubComponentAlias("ptd:HelmController", helmCtrlSubName)
		}

		helmCtrlNs, err := corev1.NewNamespace(ctx, fmt.Sprintf("%s-%s-helm-controller-namespace", name, release), &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(clustersHelmControllerNamespace),
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller namespace for %s: %w", release, err)
		}

		// HelmController CRDs
		_, err = apiextensions.NewCustomResource(ctx, fmt.Sprintf("%s-%s-helmcharts-crd", name, release), &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("apiextensions.k8s.io/v1"),
			Kind:       pulumi.String("CustomResourceDefinition"),
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helmcharts.helm.cattle.io"),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"group":                 "helm.cattle.io",
					"preserveUnknownFields": false,
					"scope":                 "Namespaced",
					"names": map[string]interface{}{
						"kind":     "HelmChart",
						"plural":   "helmcharts",
						"singular": "helmchart",
					},
					"versions": []interface{}{
						map[string]interface{}{
							"name":    "v1",
							"served":  true,
							"storage": true,
							"subresources": map[string]interface{}{
								"status": map[string]interface{}{},
							},
							"additionalPrinterColumns": []interface{}{
								map[string]interface{}{"jsonPath": ".status.jobName", "name": "Job", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.chart", "name": "Chart", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.targetNamespace", "name": "TargetNamespace", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.version", "name": "Version", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.repo", "name": "Repo", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.helmVersion", "name": "HelmVersion", "type": "string"},
								map[string]interface{}{"jsonPath": ".spec.bootstrap", "name": "Bootstrap", "type": "string"},
							},
							"schema": map[string]interface{}{
								"openAPIV3Schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"spec": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"authPassCredentials":   map[string]interface{}{"type": "boolean"},
												"authSecret":            map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"backOffLimit":          map[string]interface{}{"nullable": true, "type": "integer"},
												"bootstrap":             map[string]interface{}{"type": "boolean"},
												"chart":                 map[string]interface{}{"nullable": true, "type": "string"},
												"chartContent":          map[string]interface{}{"nullable": true, "type": "string"},
												"createNamespace":       map[string]interface{}{"type": "boolean"},
												"dockerRegistrySecret":  map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"failurePolicy":         map[string]interface{}{"nullable": true, "type": "string"},
												"helmVersion":           map[string]interface{}{"nullable": true, "type": "string"},
												"insecureSkipTLSVerify": map[string]interface{}{"type": "boolean"},
												"jobImage":              map[string]interface{}{"nullable": true, "type": "string"},
												"plainHTTP":             map[string]interface{}{"type": "boolean"},
												"podSecurityContext": map[string]interface{}{
													"nullable": true,
													"type":     "object",
													"properties": map[string]interface{}{
														"fsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
														"fsGroupChangePolicy": map[string]interface{}{"nullable": true, "type": "string"},
														"runAsGroup":          map[string]interface{}{"nullable": true, "type": "integer"},
														"runAsNonRoot":        map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsUser":           map[string]interface{}{"nullable": true, "type": "integer"},
														"seLinuxOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"level": map[string]interface{}{"nullable": true, "type": "string"},
																"role":  map[string]interface{}{"nullable": true, "type": "string"},
																"type":  map[string]interface{}{"nullable": true, "type": "string"},
																"user":  map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"seccompProfile": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
																"type":             map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"supplementalGroups": map[string]interface{}{
															"nullable": true, "type": "array",
															"items": map[string]interface{}{"type": "integer"},
														},
														"sysctls": map[string]interface{}{
															"nullable": true, "type": "array",
															"items": map[string]interface{}{
																"type": "object",
																"properties": map[string]interface{}{
																	"name":  map[string]interface{}{"nullable": true, "type": "string"},
																	"value": map[string]interface{}{"nullable": true, "type": "string"},
																},
															},
														},
														"windowsOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
																"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
																"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
																"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
													},
												},
												"repo":            map[string]interface{}{"nullable": true, "type": "string"},
												"repoCA":          map[string]interface{}{"nullable": true, "type": "string"},
												"repoCAConfigMap": map[string]interface{}{"nullable": true, "type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"nullable": true, "type": "string"}}},
												"securityContext": map[string]interface{}{
													"nullable": true,
													"type":     "object",
													"properties": map[string]interface{}{
														"allowPrivilegeEscalation": map[string]interface{}{"nullable": true, "type": "boolean"},
														"capabilities": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"add":  map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
																"drop": map[string]interface{}{"nullable": true, "type": "array", "items": map[string]interface{}{"nullable": true, "type": "string"}},
															},
														},
														"privileged":             map[string]interface{}{"nullable": true, "type": "boolean"},
														"procMount":              map[string]interface{}{"nullable": true, "type": "string"},
														"readOnlyRootFilesystem": map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsGroup":             map[string]interface{}{"nullable": true, "type": "integer"},
														"runAsNonRoot":           map[string]interface{}{"nullable": true, "type": "boolean"},
														"runAsUser":              map[string]interface{}{"nullable": true, "type": "integer"},
														"seLinuxOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"level": map[string]interface{}{"nullable": true, "type": "string"},
																"role":  map[string]interface{}{"nullable": true, "type": "string"},
																"type":  map[string]interface{}{"nullable": true, "type": "string"},
																"user":  map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"seccompProfile": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"localhostProfile": map[string]interface{}{"nullable": true, "type": "string"},
																"type":             map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
														"windowsOptions": map[string]interface{}{
															"nullable": true, "type": "object",
															"properties": map[string]interface{}{
																"gmsaCredentialSpec":     map[string]interface{}{"nullable": true, "type": "string"},
																"gmsaCredentialSpecName": map[string]interface{}{"nullable": true, "type": "string"},
																"hostProcess":            map[string]interface{}{"nullable": true, "type": "boolean"},
																"runAsUserName":          map[string]interface{}{"nullable": true, "type": "string"},
															},
														},
													},
												},
												"set":             map[string]interface{}{"nullable": true, "type": "object", "additionalProperties": map[string]interface{}{"x-kubernetes-int-or-string": true}},
												"targetNamespace": map[string]interface{}{"nullable": true, "type": "string"},
												"timeout":         map[string]interface{}{"nullable": true, "type": "string"},
												"valuesContent":   map[string]interface{}{"nullable": true, "type": "string"},
												"version":         map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
										"status": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"conditions": map[string]interface{}{
													"nullable": true, "type": "array",
													"items": map[string]interface{}{
														"type": "object",
														"properties": map[string]interface{}{
															"message": map[string]interface{}{"nullable": true, "type": "string"},
															"reason":  map[string]interface{}{"nullable": true, "type": "string"},
															"status":  map[string]interface{}{"nullable": true, "type": "string"},
															"type":    map[string]interface{}{"nullable": true, "type": "string"},
														},
													},
												},
												"jobName": map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helmcharts CRD for %s: %w", release, err)
		}

		_, err = apiextensions.NewCustomResource(ctx, fmt.Sprintf("%s-%s-helmchartconfigs", name, release), &apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String("apiextensions.k8s.io/v1"),
			Kind:       pulumi.String("CustomResourceDefinition"),
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helmchartconfigs.helm.cattle.io"),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": map[string]interface{}{
					"group":                 "helm.cattle.io",
					"preserveUnknownFields": false,
					"scope":                 "Namespaced",
					"names": map[string]interface{}{
						"kind":     "HelmChartConfig",
						"plural":   "helmchartconfigs",
						"singular": "helmchartconfig",
					},
					"versions": []interface{}{
						map[string]interface{}{
							"name":    "v1",
							"served":  true,
							"storage": true,
							"schema": map[string]interface{}{
								"openAPIV3Schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"spec": map[string]interface{}{
											"type": "object",
											"properties": map[string]interface{}{
												"failurePolicy": map[string]interface{}{"nullable": true, "type": "string"},
												"valuesContent": map[string]interface{}{"nullable": true, "type": "string"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helmchartconfigs CRD for %s: %w", release, err)
		}

		// HelmController ClusterRole + ClusterRoleBinding
		_, err = rbacv1.NewClusterRole(ctx, fmt.Sprintf("%s-%s-helm-controller-cluster-role", name, release), &rbacv1.ClusterRoleArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helm-controller"),
			},
			Rules: rbacv1.PolicyRuleArray{
				&rbacv1.PolicyRuleArgs{
					ApiGroups: pulumi.StringArray{pulumi.String("*")},
					Resources: pulumi.StringArray{pulumi.String("*")},
					Verbs:     pulumi.StringArray{pulumi.String("*")},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller ClusterRole for %s: %w", release, err)
		}

		_, err = rbacv1.NewClusterRoleBinding(ctx, fmt.Sprintf("%s-%s-helm-controller-cluster-role-binding", name, release), &rbacv1.ClusterRoleBindingArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String("helm-controller"),
			},
			RoleRef: &rbacv1.RoleRefArgs{
				ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
				Kind:     pulumi.String("ClusterRole"),
				Name:     pulumi.String("helm-controller"),
			},
			Subjects: rbacv1.SubjectArray{
				&rbacv1.SubjectArgs{
					Kind:      pulumi.String("ServiceAccount"),
					Name:      pulumi.String("default"),
					Namespace: pulumi.String(clustersHelmControllerNamespace),
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller ClusterRoleBinding for %s: %w", release, err)
		}

		// HelmController Deployment
		_, err = appsv1.NewDeployment(ctx, fmt.Sprintf("%s-%s-helm-controller-deployment", name, release), &appsv1.DeploymentArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Namespace: pulumi.String(clustersHelmControllerNamespace),
				Name:      pulumi.String("helm-controller"),
				Labels: pulumi.StringMap{
					"app": pulumi.String("helm-controller"),
				},
			},
			Spec: &appsv1.DeploymentSpecArgs{
				Replicas: pulumi.Int(1),
				Selector: &metav1.LabelSelectorArgs{
					MatchLabels: pulumi.StringMap{
						"app": pulumi.String("helm-controller"),
					},
				},
				Template: &corev1.PodTemplateSpecArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Labels: pulumi.StringMap{
							"app": pulumi.String("helm-controller"),
						},
					},
					Spec: &corev1.PodSpecArgs{
						Containers: corev1.ContainerArray{
							&corev1.ContainerArgs{
								Name:    pulumi.String("helm-controller"),
								Image:   pulumi.String("ghcr.io/k3s-io/helm-controller:v0.16.10"),
								Command: pulumi.StringArray{pulumi.String("helm-controller")},
								Args: pulumi.StringArray{
									pulumi.String("--namespace"),
									pulumi.String(clustersHelmControllerNamespace),
									pulumi.String("--default-job-image"),
									pulumi.String("ghcr.io/k3s-io/klipper-helm:latest"),
								},
							},
						},
					},
				},
			},
		}, k8sProviderOpt, withHelmCtrlAlias(), pulumi.DependsOn([]pulumi.Resource{helmCtrlNs}))
		if err != nil {
			return fmt.Errorf("clusters: failed to create helm-controller Deployment for %s: %w", release, err)
		}

		// ── CertManager (optional) ─────────────────────────────────────────────
		// Python: CertManager component name is "{compound_name}-cert-manager" (no release suffix).
		// It creates: UserAssignedIdentity, DNS contributor RoleAssignment, ServiceAccount,
		// FederatedIdentityCredential, Namespace, Helm release, ClusterIssuer per domain.
		if clusterCfg.UseLetsEncrypt {
			certMgrSubName := fmt.Sprintf("%s-cert-manager", name)
			withCertMgrAlias := func() pulumi.ResourceOption {
				return withSubComponentAlias("ptd:CertManager", certMgrSubName)
			}

			// Managed identity for cert-manager.
			// Note: ResourceName is NOT set explicitly — Python also let Pulumi auto-name it,
			// resulting in the random suffix in state (e.g., "id-...-sa52b8c9ff").
			// Python uses workload.required_tags (posit.team:true-name, posit.team:environment + resource_tags),
			// NOT buildResourceTags which adds Owner:"ptd".
			certMgrIdentityName := fmt.Sprintf("id-%s-%s-cert-manager-sa", name, release)
			certMgrIdentity, err := azmanagedidentity.NewUserAssignedIdentity(ctx,
				certMgrIdentityName,
				&azmanagedidentity.UserAssignedIdentityArgs{
					ResourceGroupName: pulumi.String(params.resourceGroupName),
					Location:          pulumi.String(params.region),
					Tags:              buildAzureRequiredTags(name, params.resourceTags),
				}, withCertMgrAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create cert-manager identity for %s: %w", release, err)
			}

			// DNS Zone Contributor role for the cert-manager identity
			_, err = azauthorization.NewRoleAssignment(ctx,
				fmt.Sprintf("%s-%s-dns-contributor-cert-manager", name, release),
				&azauthorization.RoleAssignmentArgs{
					PrincipalId:      certMgrIdentity.PrincipalId,
					PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
					RoleDefinitionId: pulumi.String(fmt.Sprintf("/providers/Microsoft.Authorization/roleDefinitions/%s", azRoleDNSZoneContributor)),
					Scope:            pulumi.String(rgScope),
				}, pulumi.Parent(certMgrIdentity))
			if err != nil {
				return fmt.Errorf("clusters: failed to create cert-manager DNS contributor role for %s: %w", release, err)
			}

			// cert-manager namespace
			certMgrNs, err := corev1.NewNamespace(ctx,
				fmt.Sprintf("%s-%s-cert-manager-namespace", name, release),
				&corev1.NamespaceArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Name: pulumi.String(clustersCertManagerNamespace),
					},
				}, k8sProviderOpt, withCertMgrAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create cert-manager namespace for %s: %w", release, err)
			}

			// cert-manager ServiceAccount (annotated with workload identity client ID)
			certMgrSA, err := corev1.NewServiceAccount(ctx,
				fmt.Sprintf("%s-%s-cert-manager-sa", name, release),
				&corev1.ServiceAccountArgs{
					Metadata: &metav1.ObjectMetaArgs{
						Name:      pulumi.String("cert-manager"),
						Namespace: pulumi.String(clustersCertManagerNamespace),
						Annotations: pulumi.StringMap{
							"azure.workload.identity/client-id": certMgrIdentity.ClientId,
						},
						Labels: pulumi.StringMap{
							"azure.workload.identity/use": pulumi.String("true"),
						},
					},
				}, k8sProviderOpt, withCertMgrAlias(),
				pulumi.DependsOn([]pulumi.Resource{certMgrNs, certMgrIdentity}))
			if err != nil {
				return fmt.Errorf("clusters: failed to create cert-manager service account for %s: %w", release, err)
			}

			// Federated identity credential for cert-manager
			if identityInfo != nil && identityInfo.OIDCIssuerURL != "" {
				_, err = azmanagedidentity.NewFederatedIdentityCredential(ctx,
					fmt.Sprintf("fedid-%s-%s-cert-manager", name, release),
					&azmanagedidentity.FederatedIdentityCredentialArgs{
						ResourceName:                            certMgrIdentity.Name,
						FederatedIdentityCredentialResourceName: pulumi.StringPtr(fmt.Sprintf("fedid-%s-%s-cert-manager", name, release)),
						ResourceGroupName:                       pulumi.String(params.resourceGroupName),
						Subject:                                 pulumi.String(fmt.Sprintf("system:serviceaccount:%s:cert-manager", clustersCertManagerNamespace)),
						Issuer:                                  pulumi.String(identityInfo.OIDCIssuerURL),
						Audiences:                               pulumi.StringArray{pulumi.String("api://AzureADTokenExchange")},
					}, pulumi.Parent(certMgrIdentity))
				if err != nil {
					return fmt.Errorf("clusters: failed to create cert-manager federated identity credential for %s: %w", release, err)
				}
			}

			// cert-manager Helm release
			certMgrHelm, err := helmv3.NewRelease(ctx,
				fmt.Sprintf("%s-%s-cert-manager", name, release),
				&helmv3.ReleaseArgs{
					Name:      pulumi.String("cert-manager"),
					Chart:     pulumi.String("cert-manager"),
					Version:   pulumi.String("v1.18.1"),
					Namespace: pulumi.String(clustersCertManagerNamespace),
					RepositoryOpts: &helmv3.RepositoryOptsArgs{
						Repo: pulumi.String("https://charts.jetstack.io"),
					},
					Atomic: pulumi.Bool(true),
					Values: pulumi.Map{
						"installCRDs": pulumi.Bool(true),
						"serviceAccount": pulumi.Map{
							"create": pulumi.Bool(false),
							"name":   pulumi.String("cert-manager"),
						},
						"podLabels": pulumi.Map{
							"azure.workload.identity/use": pulumi.String("true"),
						},
					},
				}, k8sProviderOpt, withCertMgrAlias(),
				pulumi.DependsOn([]pulumi.Resource{certMgrNs, certMgrSA}))
			if err != nil {
				return fmt.Errorf("clusters: failed to create cert-manager helm release for %s: %w", release, err)
			}

			// ClusterIssuers — one per domain
			for _, domain := range params.certManagerDomains {
				_, err = apiextensions.NewCustomResource(ctx,
					fmt.Sprintf("%s-%s-%s-cluster-issuer", name, release, domain),
					&apiextensions.CustomResourceArgs{
						ApiVersion: pulumi.String("cert-manager.io/v1"),
						Kind:       pulumi.String("ClusterIssuer"),
						Metadata: &metav1.ObjectMetaArgs{
							Name: pulumi.String(fmt.Sprintf("letsencrypt-%s", domain)),
						},
						OtherFields: kubernetes.UntypedArgs{
							"spec": map[string]interface{}{
								"acme": map[string]interface{}{
									"email":  "posit-dev@posit.co",
									"server": "https://acme-v02.api.letsencrypt.org/directory",
									"privateKeySecretRef": map[string]interface{}{
										"name": fmt.Sprintf("%s-%s-%s-letsencrypt-account-key", name, release, domain),
									},
									"solvers": []interface{}{
										map[string]interface{}{
											"dns01": map[string]interface{}{
												"azureDNS": map[string]interface{}{
													"managedIdentity": map[string]interface{}{
														"clientID": certMgrIdentity.ClientId,
													},
													"environment":       "AzurePublicCloud",
													"hostedZoneName":    domain,
													"resourceGroupName": params.resourceGroupName,
													"subscriptionID":    params.subscriptionID,
												},
											},
										},
									},
								},
							},
						},
					}, k8sProviderOpt, withCertMgrAlias(),
					pulumi.DependsOn([]pulumi.Resource{certMgrHelm}))
				if err != nil {
					return fmt.Errorf("clusters: failed to create cluster issuer for %s/%s: %w", release, domain, err)
				}
			}
		}

		// ── Traefik ────────────────────────────────────────────────────────────
		// Python: AzureTraefik component name is "{compound_name}-traefik" (no release suffix).
		traefikSubName := fmt.Sprintf("%s-traefik", name)
		withTraefikAlias := func() pulumi.ResourceOption {
			return withSubComponentAlias("ptd:AzureTraefik", traefikSubName)
		}

		_, err = corev1.NewNamespace(ctx, fmt.Sprintf("%s-%s-traefik-namespace", name, release), &corev1.NamespaceArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Name: pulumi.String(clustersTraefikNamespace),
			},
		}, k8sProviderOpt, withTraefikAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create traefik namespace for %s: %w", release, err)
		}

		// Traefik Helm release values — mirrors azure_traefik.py _define_helm_release
		traefikValues := pulumi.Map{
			"logs": pulumi.Map{
				"general": pulumi.Map{
					"level": pulumi.String("DEBUG"),
				},
			},
			"ports": pulumi.Map{
				"web": pulumi.Map{
					"redirections": pulumi.Map{
						"entryPoint": pulumi.Map{
							"to":        pulumi.String("websecure"),
							"scheme":    pulumi.String("https"),
							"permanent": pulumi.Bool(true),
						},
					},
				},
				"websecure": pulumi.Map{
					"tls": pulumi.Map{
						"enabled": pulumi.Bool(true),
					},
				},
			},
			"ingressClass": pulumi.Map{
				"enabled":        pulumi.Bool(true),
				"isDefaultClass": pulumi.Bool(true),
			},
			"ingressRoute": pulumi.Map{
				"dashboard": pulumi.Map{
					"enabled": pulumi.Bool(true),
				},
			},
			"providers": pulumi.Map{
				"kubernetesCRD": pulumi.Map{
					"enabled":             pulumi.Bool(true),
					"allowCrossNamespace": pulumi.Bool(true),
				},
				"kubernetesIngress": pulumi.Map{
					"enabled": pulumi.Bool(true),
				},
			},
			"service": pulumi.Map{
				"annotations": pulumi.Map{
					"service.beta.kubernetes.io/azure-load-balancer-internal": pulumi.String("true"),
				},
				"type": pulumi.String("LoadBalancer"),
			},
		}
		if !params.thirdPartyTelemetryEnabled {
			traefikValues["globalArguments"] = pulumi.Array{
				pulumi.String("--global.checknewversion=false"),
				pulumi.String("--global.sendanonymoususage=false"),
			}
		}

		// extraObjects: redirect middleware + one Ingress per domain.
		// Mirrors azure_traefik.py _define_redirect_middleware() and _define_ingresses().
		extraObjects := pulumi.Array{
			pulumi.Map{
				"apiVersion": pulumi.String("traefik.io/v1alpha1"),
				"kind":       pulumi.String("Middleware"),
				"metadata": pulumi.Map{
					"name":      pulumi.String("redirect-https"),
					"namespace": pulumi.String(clustersTraefikNamespace),
				},
				"spec": pulumi.Map{
					"redirectScheme": pulumi.Map{
						"scheme":    pulumi.String("https"),
						"permanent": pulumi.Bool(true),
					},
				},
			},
		}
		for _, domain := range params.certManagerDomains {
			ingressAnnotations := pulumi.Map{
				"traefik.ingress.kubernetes.io/router.middlewares": pulumi.String("traefik-redirect-https@kubernetescrd"),
			}
			if clusterCfg.UseLetsEncrypt {
				ingressAnnotations["cert-manager.io/cluster-issuer"] = pulumi.String(fmt.Sprintf("letsencrypt-%s", domain))
			}
			extraObjects = append(extraObjects, pulumi.Map{
				"apiVersion": pulumi.String("networking.k8s.io/v1"),
				"kind":       pulumi.String("Ingress"),
				"metadata": pulumi.Map{
					"name":        pulumi.String(fmt.Sprintf("%s-%s-%s", name, release, domain)),
					"labels":      pulumi.Map{"app": pulumi.String("traefik")},
					"namespace":   pulumi.String(clustersTraefikNamespace),
					"annotations": ingressAnnotations,
				},
				"spec": pulumi.Map{
					"ingressClassName": pulumi.String("traefik"),
					"tls": pulumi.Array{
						pulumi.Map{
							"hosts":      pulumi.StringArray{pulumi.String(domain), pulumi.String(fmt.Sprintf("*.%s", domain))},
							"secretName": pulumi.String(fmt.Sprintf("%s-%s-tls", name, domain)),
						},
					},
					"rules": pulumi.Array{
						pulumi.Map{
							"http": pulumi.Map{
								"paths": pulumi.Array{
									pulumi.Map{
										"path":     pulumi.String("/*"),
										"pathType": pulumi.String("ImplementationSpecific"),
										"backend": pulumi.Map{
											"service": pulumi.Map{
												"name": pulumi.String("traefik"),
												"port": pulumi.Map{
													"number": pulumi.Int(80),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			})
		}
		traefikValues["extraObjects"] = extraObjects

		_, err = helmv3.NewRelease(ctx, fmt.Sprintf("%s-%s-traefik", name, release), &helmv3.ReleaseArgs{
			Name:      pulumi.String("traefik"),
			Chart:     pulumi.String("traefik"),
			Version:   pulumi.String("33.2.1"),
			Namespace: pulumi.String(clustersTraefikNamespace),
			RepositoryOpts: &helmv3.RepositoryOptsArgs{
				Repo: pulumi.String("https://traefik.github.io/charts"),
			},
			Atomic: pulumi.Bool(true),
			Values: traefikValues,
		}, k8sProviderOpt, withTraefikAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create traefik helm release for %s: %w", release, err)
		}

		// ── AzureFilesCSI ──────────────────────────────────────────────────────
		// Python: AzureFilesCSI component name is "{compound_name}-{release}-azure-files-csi".
		azFilesSubName := fmt.Sprintf("%s-%s-azure-files-csi", name, release)
		withAzFilesAlias := func() pulumi.ResourceOption {
			return withSubComponentAlias("ptd:AzureFilesCSI", azFilesSubName)
		}

		// Files CSI role assignment — cluster system identity needs Storage Account Contributor
		// on the specific storage account.
		// Python azure_files_csi.py uses cluster.identity.principal_id which is the
		// system-assigned identity of the AKS cluster (NOT the kubelet identity).
		if identityInfo != nil && identityInfo.ClusterPrincipalID != "" {
			storageScope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
				params.subscriptionID, params.resourceGroupName, params.azureFilesStorageAccountName)
			_, err = azauthorization.NewRoleAssignment(ctx,
				fmt.Sprintf("%s-%s-files-csi-role", name, release),
				&azauthorization.RoleAssignmentArgs{
					PrincipalId:   pulumi.String(identityInfo.ClusterPrincipalID),
					PrincipalType: pulumi.StringPtr("ServicePrincipal"),
					RoleDefinitionId: pulumi.String(fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
						params.subscriptionID, azRoleStorageAccountContributor)),
					Scope: pulumi.String(storageScope),
				}, withAzFilesAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create files-csi role assignment for %s: %w", release, err)
			}
		}

		// Azure Files CSI StorageClass.
		// Python uses workload.azure_files_csi_storage_class_name = f"{compound_name}-azure-files-csi".
		azFilesStorageClassName := fmt.Sprintf("%s-azure-files-csi", name)
		_, err = apiextensions.NewCustomResource(ctx,
			fmt.Sprintf("%s-%s-azure-files-csi", name, release),
			&apiextensions.CustomResourceArgs{
				ApiVersion: pulumi.String("storage.k8s.io/v1"),
				Kind:       pulumi.String("StorageClass"),
				Metadata: &metav1.ObjectMetaArgs{
					Name: pulumi.String(azFilesStorageClassName),
				},
				OtherFields: kubernetes.UntypedArgs{
					"provisioner": "file.csi.azure.com",
					"parameters": map[string]interface{}{
						"resourceGroup":   params.resourceGroupName,
						"storageAccount":  params.azureFilesStorageAccountName,
						"server":          fmt.Sprintf("%s.file.core.windows.net", params.azureFilesStorageAccountName),
						"shareNamePrefix": "ppm-",
						"protocol":        "nfs",
					},
					"mountOptions": []interface{}{
						"nconnect=4",
						"noresvport",
						"actimeo=30",
						"lookupcache=pos",
					},
					"allowVolumeExpansion": true,
					"reclaimPolicy":        "Retain",
					"volumeBindingMode":    "Immediate",
				},
			}, k8sProviderOpt, withAzFilesAlias())
		if err != nil {
			return fmt.Errorf("clusters: failed to create azure-files-csi storage class for %s: %w", release, err)
		}

		// ── Bastion NSG ─────────────────────────────────────────────────────────
		// Python: NetworkSecurityGroup for bastion-AKS communication, direct child of AzureWorkloadClusters.
		// Python logical name: "{release}-bastion-aks-nsg"
		// Python NSG resource name: "{compound_name}-{release}-bastion-access"
		// Requires VNet subnet info from the cluster's agentPoolProfiles.
		if identityInfo != nil && identityInfo.VNetSubnetID != "" {
			// Parse VNet info from subnet ID:
			// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet}
			vnetParts := strings.Split(identityInfo.VNetSubnetID, "/")
			if len(vnetParts) >= 11 {
				vnetResourceGroup := vnetParts[4]
				vnetName := vnetParts[8]
				aksSubnetName := vnetParts[10]
				nsgName := fmt.Sprintf("%s-%s-bastion-access", name, release)
				nsgLogicalName := fmt.Sprintf("%s-bastion-aks-nsg", release)
				clusterLocation := params.region
				err = azureCreateBastionNSG(ctx,
					nsgLogicalName, nsgName,
					params.resourceGroupName, clusterLocation,
					vnetResourceGroup, vnetName, aksSubnetName,
					release, name,
					withAlias(),
				)
				if err != nil {
					return fmt.Errorf("clusters: failed to create bastion NSG for %s: %w", release, err)
				}
			}
		}

		// ── CoreDNS forwarding (optional) ──────────────────────────────────────
		// Python: uses kubernetes.core.v1.ConfigMapPatch with replace_on_changes=[].
		// In Go Pulumi SDK, we use a custom K8s resource with server-side apply semantics.
		// DNS forwarding entries are direct children of AzureWorkloadClusters.
		if len(params.dnsForwardDomains) > 0 {
			dnsData := pulumi.StringMap{}
			for _, domain := range params.dnsForwardDomains {
				key := fmt.Sprintf("dns-forward-%s.server", strings.ReplaceAll(domain.Host, ".", "-"))
				val := fmt.Sprintf("%s:53 {\n  errors\n  cache 30\n  forward . %s\n}\n", domain.Host, domain.IP)
				dnsData[key] = pulumi.String(val)
			}
			_, err = apiextensions.NewCustomResource(ctx,
				fmt.Sprintf("%s-%s-coredns-forward", name, release),
				&apiextensions.CustomResourceArgs{
					ApiVersion: pulumi.String("v1"),
					Kind:       pulumi.String("ConfigMap"),
					Metadata: &metav1.ObjectMetaArgs{
						Name:      pulumi.String("coredns-custom"),
						Namespace: pulumi.String(clustersKubeSystemNamespace),
					},
					OtherFields: kubernetes.UntypedArgs{
						"data": dnsData,
					},
				}, k8sProviderOpt, withAlias())
			if err != nil {
				return fmt.Errorf("clusters: failed to create coredns forwarding configmap for %s: %w", release, err)
			}
		}

		_ = withSubComponentAlias // suppress unused warning; used throughout above

		// ── Custom K8s resources (optional, per-cluster) ─────────────────────────
		if err := createCustomK8sResources(ctx, params.workloadDir, release,
			params.clusters[release].CustomK8sResources, k8sProviderOpt, withAlias()); err != nil {
			return err
		}
	}

	return nil
}

// azureCreateBastionNSG creates an NSG for bastion-AKS communication.
// It looks up both the AzureBastionSubnet and the AKS subnet CIDRs via the Pulumi
// azure-native network SDK and creates the NSG with appropriate security rules.
// Returns nil without creating anything if VNet info is unavailable.
func azureCreateBastionNSG(
	ctx *pulumi.Context,
	logicalName string,
	nsgName string,
	resourceGroup string,
	location string,
	vnetResourceGroup string,
	vnetName string,
	aksSubnetName string,
	release string,
	compoundName string,
	opts ...pulumi.ResourceOption,
) error {
	bastionSubnet, err := aznetwork.LookupSubnet(ctx, &aznetwork.LookupSubnetArgs{
		ResourceGroupName:  vnetResourceGroup,
		VirtualNetworkName: vnetName,
		SubnetName:         "AzureBastionSubnet",
	})
	if err != nil {
		// If we can't look up the bastion subnet, skip NSG creation (mirrors Python behavior).
		return nil
	}

	aksSubnet, err := aznetwork.LookupSubnet(ctx, &aznetwork.LookupSubnetArgs{
		ResourceGroupName:  vnetResourceGroup,
		VirtualNetworkName: vnetName,
		SubnetName:         aksSubnetName,
	})
	if err != nil {
		return nil
	}

	bastionCIDR := ""
	if bastionSubnet.AddressPrefix != nil {
		bastionCIDR = *bastionSubnet.AddressPrefix
	}
	aksCIDR := ""
	if aksSubnet.AddressPrefix != nil {
		aksCIDR = *aksSubnet.AddressPrefix
	}
	if bastionCIDR == "" || aksCIDR == "" {
		return nil
	}

	// Build tags matching Python: posit.team/* tags + NSG-specific tags.
	// Python does NOT include Owner/generic resource tags on the NSG — only the specific
	// posit.team/* keys (derived from workload.required_tags) plus NSG-specific keys.
	// The compound name is "{trueName}-{environment}"; split on the last "-".
	trueName, environment := compoundName, ""
	if idx := strings.LastIndex(compoundName, "-"); idx >= 0 {
		trueName = compoundName[:idx]
		environment = compoundName[idx+1:]
	}
	tags := pulumi.StringMap{}
	tags["posit.team:true-name"] = pulumi.String(trueName)
	tags["posit.team:environment"] = pulumi.String(environment)
	tags["posit.team:managed-by"] = pulumi.String("ptd.pulumi_resources.azure_workload_clusters")
	tags["Name"] = pulumi.String(nsgName)
	tags["Purpose"] = pulumi.String("AKS-Bastion-Access")
	tags["Release"] = pulumi.String(release)
	tags["BastionSubnetCIDR"] = pulumi.String(bastionCIDR)
	tags["AKSSubnetCIDR"] = pulumi.String(aksCIDR)

	_, err = aznetwork.NewNetworkSecurityGroup(ctx, logicalName, &aznetwork.NetworkSecurityGroupArgs{
		ResourceGroupName:        pulumi.String(resourceGroup),
		Location:                 pulumi.String(location),
		NetworkSecurityGroupName: pulumi.String(nsgName),
		SecurityRules: aznetwork.SecurityRuleTypeArray{
			&aznetwork.SecurityRuleTypeArgs{
				Name:                     pulumi.StringPtr("AllowBastionToAKS"),
				Priority:                 pulumi.IntPtr(1000),
				Direction:                pulumi.String("Inbound"),
				Access:                   pulumi.String("Allow"),
				Protocol:                 pulumi.String("*"),
				SourcePortRange:          pulumi.StringPtr("*"),
				DestinationPortRange:     pulumi.StringPtr("*"),
				SourceAddressPrefix:      pulumi.StringPtr(bastionCIDR),
				DestinationAddressPrefix: pulumi.StringPtr(aksCIDR),
				Description:              pulumi.StringPtr("Allow all traffic from Bastion to AKS cluster subnet only"),
			},
			&aznetwork.SecurityRuleTypeArgs{
				Name:                     pulumi.StringPtr("AllowAKSToBastion"),
				Priority:                 pulumi.IntPtr(1010),
				Direction:                pulumi.String("Outbound"),
				Access:                   pulumi.String("Allow"),
				Protocol:                 pulumi.String("*"),
				SourcePortRange:          pulumi.StringPtr("*"),
				DestinationPortRange:     pulumi.StringPtr("*"),
				SourceAddressPrefix:      pulumi.StringPtr(aksCIDR),
				DestinationAddressPrefix: pulumi.StringPtr(bastionCIDR),
				Description:              pulumi.StringPtr("Allow all traffic from AKS cluster subnet to Bastion"),
			},
		},
		Tags: tags,
	}, opts...)
	return err
}
