package steps

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/posit-dev/ptd/lib/azure"
	"github.com/posit-dev/ptd/lib/helpers"
	"github.com/posit-dev/ptd/lib/kube"
	"github.com/posit-dev/ptd/lib/types"
	azauthorization "github.com/pulumi/pulumi-azure-native-sdk/authorization/v3"
	azmanagedidentity "github.com/pulumi/pulumi-azure-native-sdk/managedidentity/v3"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	apiextensions "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	yaml "gopkg.in/yaml.v3"
)

// Role UUIDs used by Azure helm resources (not already in clusters.go constants).
const (
	azRoleStorageBlobDataContributor = "ba92f5b4-2d11-453d-a403-e96b0029c9fe"
	azRoleMonitoringReader           = "43d0d8ad-25c7-4714-9337-8ba259a9fe05"
)

// Service account names used in Azure helm deployments (mirror Python Roles enum).
const (
	azureRoleLoki        = "loki.posit.team"
	azureRoleMimir       = "mimir.posit.team"
	azureRoleAlloy       = "alloy.posit.team"
	azureRoleExternalDNS = "external-dns.posit.team"
)

// azureHelmParams bundles pre-fetched data for the Azure helm deploy function.
type azureHelmParams struct {
	compoundName           string
	trueName               string // derived: everything before last "-" in compoundName
	environment            string // derived: last segment of compoundName split by "-"
	subscriptionID         string
	tenantID               string
	region                 string
	resourceGroupName      string
	storageAccountName     string // "stptd" + sanitized compound name (no hyphens, max 24 chars)
	kubeconfigsByCluster   map[string]string
	oidcIssuerURLByCluster map[string]string
	cfg                    types.AzureWorkloadConfig
	mimirPassword          string // fetched from Key Vault: "{compoundName}-mimir-auth"
	grafanaAdminFQDN       string // fetched from Key Vault: "{compoundName}-grafana-postgres-admin-secret"
	// per-cluster grafana DB URL (from Key Vault per-cluster secrets)
	grafanaDBURLByCluster map[string]string
	workloadDir           string
}

func (s *HelmStep) runAzureInlineGo(ctx context.Context, creds types.Credentials, envVars map[string]string) error {
	rawConfig, err := helpers.ConfigForTarget(s.DstTarget)
	if err != nil {
		return fmt.Errorf("helm azure: failed to load config: %w", err)
	}
	cfg, ok := rawConfig.(types.AzureWorkloadConfig)
	if !ok {
		return fmt.Errorf("helm azure: expected AzureWorkloadConfig")
	}

	azureTarget, ok := s.DstTarget.(azure.Target)
	if !ok {
		return fmt.Errorf("helm azure: expected Azure target")
	}

	azCreds, err := azure.OnlyAzureCredentials(creds)
	if err != nil {
		return err
	}

	// Build per-cluster kubeconfigs and OIDC issuer URLs.
	kubeconfigsByCluster := make(map[string]string, len(cfg.Clusters))
	oidcIssuerURLByCluster := make(map[string]string, len(cfg.Clusters))
	for release := range cfg.Clusters {
		clusterName := s.DstTarget.Name() + "-" + release
		kubeconfigBytes, clusterErr := azure.GetKubeCredentials(
			ctx, azCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), clusterName,
		)
		if clusterErr != nil {
			return fmt.Errorf("helm azure: failed to get AKS kubeconfig for %s: %w", clusterName, clusterErr)
		}
		if !s.DstTarget.TailscaleEnabled() {
			kubeconfigBytes, clusterErr = kube.AddProxyToKubeConfigBytes(kubeconfigBytes, "socks5://localhost:1080")
			if clusterErr != nil {
				return fmt.Errorf("helm azure: failed to add proxy to kubeconfig for %s: %w", clusterName, clusterErr)
			}
		}
		kubeconfigsByCluster[release] = string(kubeconfigBytes)

		identityInfo, clusterErr := azure.GetClusterIdentityInfo(
			ctx, azCreds, azureTarget.SubscriptionID(), azureTarget.ResourceGroupName(), clusterName,
		)
		if clusterErr != nil {
			return fmt.Errorf("helm azure: failed to get cluster identity info for %s: %w", clusterName, clusterErr)
		}
		if identityInfo != nil {
			oidcIssuerURLByCluster[release] = identityInfo.OIDCIssuerURL
		}
	}

	// Compute storage account name: matches Python's AzureWorkload.storage_account_name.
	sanitizedStorageName := strings.ToLower(s.DstTarget.Name())
	sanitizedStorageName = strings.ReplaceAll(sanitizedStorageName, "-", "")
	if len(sanitizedStorageName) > 19 {
		sanitizedStorageName = sanitizedStorageName[:19]
	}
	storageAccountName := "stptd" + sanitizedStorageName

	// Fetch mimir auth password from Key Vault.
	mimirPassword := ""
	mimirSecretName := s.DstTarget.Name() + "-mimir-auth"
	if mimirPw, secretErr := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, mimirSecretName); secretErr != nil {
		fmt.Printf("helm azure: warning: failed to get mimir secret %q: %v\n", mimirSecretName, secretErr)
	} else {
		mimirPassword = mimirPw
	}

	// Fetch grafana admin secret for fqdn.
	grafanaAdminFQDN := ""
	grafanaAdminSecretName := s.DstTarget.Name() + "-grafana-postgres-admin-secret"
	if grafanaAdminSecretJSON, secretErr := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, grafanaAdminSecretName); secretErr != nil {
		fmt.Printf("helm azure: warning: failed to get grafana admin secret %q: %v\n", grafanaAdminSecretName, secretErr)
	} else {
		var adminSecret map[string]string
		if jsonErr := json.Unmarshal([]byte(grafanaAdminSecretJSON), &adminSecret); jsonErr == nil {
			grafanaAdminFQDN = adminSecret["fqdn"]
		}
	}

	// Fetch per-cluster grafana DB URL secrets from Key Vault.
	grafanaDBURLByCluster := make(map[string]string, len(cfg.Clusters))
	for release := range cfg.Clusters {
		secretName := s.DstTarget.Name() + "-" + release + "-postgres-grafana-user"
		secretJSON, secretErr := s.DstTarget.SecretStore().GetSecretValue(ctx, creds, secretName)
		if secretErr != nil {
			fmt.Printf("helm azure: warning: failed to get grafana DB secret %q for release %s: %v\n", secretName, release, secretErr)
			continue
		}
		var dbSecret map[string]string
		if jsonErr := json.Unmarshal([]byte(secretJSON), &dbSecret); jsonErr != nil {
			fmt.Printf("helm azure: warning: failed to parse grafana DB secret for release %s: %v\n", release, jsonErr)
			continue
		}
		role := dbSecret["role"]
		database := dbSecret["database"]
		pw := dbSecret["password"]
		if role != "" && database != "" && pw != "" && grafanaAdminFQDN != "" {
			grafanaDBURLByCluster[release] = fmt.Sprintf("postgres://%s:%s@%s/%s", role, pw, grafanaAdminFQDN, database)
		}
	}

	trueName, environment := splitCompoundName(s.DstTarget.Name())

	params := azureHelmParams{
		compoundName:           s.DstTarget.Name(),
		trueName:               trueName,
		environment:            environment,
		subscriptionID:         azureTarget.SubscriptionID(),
		tenantID:               cfg.TenantID,
		region:                 s.DstTarget.Region(),
		resourceGroupName:      azureTarget.ResourceGroupName(),
		storageAccountName:     storageAccountName,
		kubeconfigsByCluster:   kubeconfigsByCluster,
		oidcIssuerURLByCluster: oidcIssuerURLByCluster,
		cfg:                    cfg,
		mimirPassword:          mimirPassword,
		grafanaAdminFQDN:       grafanaAdminFQDN,
		grafanaDBURLByCluster:  grafanaDBURLByCluster,
		workloadDir:            filepath.Join(helpers.GetTargetsConfigPath(), helpers.WorkDir, s.DstTarget.Name()),
	}

	stack, err := createStack(ctx, s.Name(), s.DstTarget, func(pctx *pulumi.Context, _ types.Target) error {
		return azureHelmDeploy(pctx, params)
	}, envVars)
	if err != nil {
		return err
	}
	return runPulumi(ctx, stack, s.Options)
}

// azureHelmDeploy is the package-level Azure deploy function, callable from tests.
func azureHelmDeploy(ctx *pulumi.Context, params azureHelmParams) error {
	name := params.compoundName

	// All resources in Python were direct children of AzureWorkloadHelm component.
	outerProject := "ptd-azure-workload-helm"

	// withAlias returns an alias from the old Python project URN.
	withAlias := func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf("urn:pulumi:%s::%s::ptd:AzureWorkloadHelm$%s::%s",
			ctx.Stack(), outerProject, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// withNestedAlias returns an alias for resources nested under a root-level Python ComponentResource.
	// AlloyConfig was instantiated without a parent, so its children's URNs are just
	// parentType$resourceType (NOT AzureWorkloadHelm$parentType$...).
	withNestedAlias := func(parentType, resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), outerProject, parentType, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	releases := helpers.SortedKeys(params.cfg.Clusters)

	for _, release := range releases {
		clusterCfg := params.cfg.Clusters[release]
		resolved := clusterCfg.Components.ResolveAzureComponents()

		k8sProviderName := name + "-" + release
		k8sProvider, err := kubernetes.NewProvider(ctx, k8sProviderName, &kubernetes.ProviderArgs{
			Kubeconfig: pulumi.String(params.kubeconfigsByCluster[release]),
		}, withAlias("pulumi:providers:kubernetes", k8sProviderName),
			pulumi.IgnoreChanges([]string{"kubeconfig"}))
		if err != nil {
			return fmt.Errorf("helm azure: failed to create k8s provider for %s: %w", release, err)
		}
		k8sOpt := pulumi.Provider(k8sProvider)

		oidcIssuerURL := params.oidcIssuerURLByCluster[release]

		// 1. External DNS
		if err := azureHelmExternalDNS(ctx, k8sOpt, name, release, params, oidcIssuerURL, resolved.ExternalDnsVersion, withAlias); err != nil {
			return err
		}

		// 2. Loki
		if err := azureHelmLoki(ctx, k8sOpt, name, release, params, oidcIssuerURL, resolved.LokiVersion, withAlias); err != nil {
			return err
		}

		// 3. Mimir
		if err := azureHelmMimir(ctx, k8sOpt, name, release, params, oidcIssuerURL, resolved.MimirVersion, withAlias); err != nil {
			return err
		}

		// 4. Grafana
		if err := azureHelmGrafana(ctx, k8sOpt, name, release, params, resolved.GrafanaVersion, withAlias); err != nil {
			return err
		}

		// 5. Alloy
		if err := azureHelmAlloy(ctx, k8sOpt, name, release, params, oidcIssuerURL, resolved.AlloyVersion, withAlias, withNestedAlias); err != nil {
			return err
		}

		// 6. Kube State Metrics
		if err := azureHelmKubeStateMetrics(ctx, k8sOpt, name, release, resolved.KubeStateMetricsVersion, withAlias); err != nil {
			return err
		}

		// 7. Nvidia Device Plugin (only when GPU nodes are enabled)
		if params.cfg.NvidiaGpuEnabled {
			if err := azureHelmNvidiaDevicePlugin(ctx, k8sOpt, name, release, resolved.NvidiaDevicePluginVersion, withAlias); err != nil {
				return err
			}
		}
	}

	return nil
}

// azureHelmBlobStorageIdentity creates a managed identity with Storage Blob Data Contributor role
// and a federated identity credential. Returns the identity resource.
func azureHelmBlobStorageIdentity(
	ctx *pulumi.Context,
	compoundName, release, component, namespace, serviceAccount string,
	params azureHelmParams,
	oidcIssuerURL string,
	withAlias func(string, string) pulumi.ResourceOption,
) (*azmanagedidentity.UserAssignedIdentity, error) {

	identityResourceName := fmt.Sprintf("id-%s-%s-%s", compoundName, release, component)
	identity, err := azmanagedidentity.NewUserAssignedIdentity(ctx, identityResourceName, &azmanagedidentity.UserAssignedIdentityArgs{
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Location:          pulumi.String(params.region),
		Tags:              buildAzureRequiredTags(compoundName, params.cfg.ResourceTags),
	}, withAlias("azure-native:managedidentity:UserAssignedIdentity", identityResourceName))
	if err != nil {
		return nil, fmt.Errorf("helm azure: failed to create %s identity for %s: %w", component, release, err)
	}

	// Storage Blob Data Contributor on the storage account.
	storageScope := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		params.subscriptionID, params.resourceGroupName, params.storageAccountName,
	)
	blobRoleResourceName := fmt.Sprintf("%s-%s-%s-blob-contributor", compoundName, release, component)
	_, err = azauthorization.NewRoleAssignment(ctx, blobRoleResourceName, &azauthorization.RoleAssignmentArgs{
		Scope:            pulumi.String(storageScope),
		PrincipalId:      identity.PrincipalId,
		RoleDefinitionId: pulumi.String(fmt.Sprintf("/providers/Microsoft.Authorization/roleDefinitions/%s", azRoleStorageBlobDataContributor)),
		PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
	}, pulumi.Parent(identity),
		withAlias("azure-native:authorization:RoleAssignment", blobRoleResourceName))
	if err != nil {
		return nil, fmt.Errorf("helm azure: failed to create %s blob role for %s: %w", component, release, err)
	}

	// Federated identity credential for workload identity.
	if oidcIssuerURL != "" {
		fedIDResourceName := fmt.Sprintf("fedid-%s-%s-%s", compoundName, release, component)
		_, err = azmanagedidentity.NewFederatedIdentityCredential(ctx, fedIDResourceName, &azmanagedidentity.FederatedIdentityCredentialArgs{
			ResourceName:                            identity.Name,
			FederatedIdentityCredentialResourceName: pulumi.StringPtr(fedIDResourceName),
			ResourceGroupName:                       pulumi.String(params.resourceGroupName),
			Subject:                                 pulumi.String(fmt.Sprintf("system:serviceaccount:%s:%s", namespace, serviceAccount)),
			Issuer:                                  pulumi.String(oidcIssuerURL),
			Audiences:                               pulumi.StringArray{pulumi.String("api://AzureADTokenExchange")},
		}, pulumi.Parent(identity),
			withAlias("azure-native:managedidentity:FederatedIdentityCredential", fedIDResourceName))
		if err != nil {
			return nil, fmt.Errorf("helm azure: failed to create %s federated credential for %s: %w", component, release, err)
		}
	}

	return identity, nil
}

// azureHelmAlloyMonitoringIdentity creates a managed identity with Monitoring Reader role
// and a federated identity credential for Alloy.
func azureHelmAlloyMonitoringIdentity(
	ctx *pulumi.Context,
	compoundName, release string,
	params azureHelmParams,
	oidcIssuerURL string,
	withAlias func(string, string) pulumi.ResourceOption,
) (*azmanagedidentity.UserAssignedIdentity, error) {

	identityResourceName := fmt.Sprintf("id-%s-%s-alloy", compoundName, release)
	identity, err := azmanagedidentity.NewUserAssignedIdentity(ctx, identityResourceName, &azmanagedidentity.UserAssignedIdentityArgs{
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Location:          pulumi.String(params.region),
		Tags:              buildAzureRequiredTags(compoundName, params.cfg.ResourceTags),
	}, withAlias("azure-native:managedidentity:UserAssignedIdentity", identityResourceName))
	if err != nil {
		return nil, fmt.Errorf("helm azure: failed to create alloy identity for %s: %w", release, err)
	}

	// Monitoring Reader role on the resource group.
	rgScope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", params.subscriptionID, params.resourceGroupName)
	monitorRoleResourceName := fmt.Sprintf("%s-%s-alloy-monitoring-reader", compoundName, release)
	_, err = azauthorization.NewRoleAssignment(ctx, monitorRoleResourceName, &azauthorization.RoleAssignmentArgs{
		Scope:            pulumi.String(rgScope),
		PrincipalId:      identity.PrincipalId,
		RoleDefinitionId: pulumi.String(fmt.Sprintf("/providers/Microsoft.Authorization/roleDefinitions/%s", azRoleMonitoringReader)),
		PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
	}, pulumi.Parent(identity),
		withAlias("azure-native:authorization:RoleAssignment", monitorRoleResourceName))
	if err != nil {
		return nil, fmt.Errorf("helm azure: failed to create alloy monitoring reader role for %s: %w", release, err)
	}

	// Federated identity credential.
	if oidcIssuerURL != "" {
		fedIDResourceName := fmt.Sprintf("fedid-%s-%s-alloy", compoundName, release)
		_, err = azmanagedidentity.NewFederatedIdentityCredential(ctx, fedIDResourceName, &azmanagedidentity.FederatedIdentityCredentialArgs{
			ResourceName:                            identity.Name,
			FederatedIdentityCredentialResourceName: pulumi.StringPtr(fedIDResourceName),
			ResourceGroupName:                       pulumi.String(params.resourceGroupName),
			Subject:                                 pulumi.String(fmt.Sprintf("system:serviceaccount:%s:%s", helmAlloyNamespace, azureRoleAlloy)),
			Issuer:                                  pulumi.String(oidcIssuerURL),
			Audiences:                               pulumi.StringArray{pulumi.String("api://AzureADTokenExchange")},
		}, pulumi.Parent(identity),
			withAlias("azure-native:managedidentity:FederatedIdentityCredential", fedIDResourceName))
		if err != nil {
			return nil, fmt.Errorf("helm azure: failed to create alloy federated credential for %s: %w", release, err)
		}
	}

	return identity, nil
}

func azureHelmExternalDNS(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params azureHelmParams, oidcIssuerURL, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	// Create managed identity.
	identityResourceName := fmt.Sprintf("id-%s-%s-external-dns", compoundName, release)
	identity, err := azmanagedidentity.NewUserAssignedIdentity(ctx, identityResourceName, &azmanagedidentity.UserAssignedIdentityArgs{
		ResourceGroupName: pulumi.String(params.resourceGroupName),
		Location:          pulumi.String(params.region),
		Tags:              buildAzureRequiredTags(compoundName, params.cfg.ResourceTags),
	}, withAlias("azure-native:managedidentity:UserAssignedIdentity", identityResourceName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create external-dns identity for %s: %w", release, err)
	}

	// DNS Zone Contributor role assignment — per root_domain or per site.
	if params.cfg.RootDomain != nil && *params.cfg.RootDomain != "" {
		scope := fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnszones/%s",
			params.subscriptionID, params.resourceGroupName, *params.cfg.RootDomain,
		)
		roleResourceName := fmt.Sprintf("%s-%s-external-dns-dns-contributor", compoundName, release)
		_, err = azauthorization.NewRoleAssignment(ctx, roleResourceName, &azauthorization.RoleAssignmentArgs{
			Scope:            pulumi.String(scope),
			PrincipalId:      identity.PrincipalId,
			RoleDefinitionId: pulumi.String(fmt.Sprintf("/providers/Microsoft.Authorization/roleDefinitions/%s", azRoleDNSZoneContributor)),
			PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
		}, pulumi.Parent(identity),
			withAlias("azure-native:authorization:RoleAssignment", roleResourceName))
		if err != nil {
			return fmt.Errorf("helm azure: failed to create external-dns dns-contributor for %s: %w", release, err)
		}
	} else {
		for siteName, siteConfig := range params.cfg.Sites {
			scope := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnszones/%s",
				params.subscriptionID, params.resourceGroupName, siteConfig.Spec.Domain,
			)
			roleResourceName := fmt.Sprintf("%s-%s-%s-external-dns-dns-contributor", compoundName, release, siteName)
			_, roleErr := azauthorization.NewRoleAssignment(ctx, roleResourceName, &azauthorization.RoleAssignmentArgs{
				Scope:            pulumi.String(scope),
				PrincipalId:      identity.PrincipalId,
				RoleDefinitionId: pulumi.String(fmt.Sprintf("/providers/Microsoft.Authorization/roleDefinitions/%s", azRoleDNSZoneContributor)),
				PrincipalType:    pulumi.StringPtr("ServicePrincipal"),
			}, pulumi.Parent(identity),
				withAlias("azure-native:authorization:RoleAssignment", roleResourceName))
			if roleErr != nil {
				return fmt.Errorf("helm azure: failed to create external-dns dns-contributor for site %s/%s: %w", release, siteName, roleErr)
			}
		}
	}

	// Federated identity credential.
	if oidcIssuerURL != "" {
		fedIDResourceName := fmt.Sprintf("fedid-%s-%s-external-dns", compoundName, release)
		_, err = azmanagedidentity.NewFederatedIdentityCredential(ctx, fedIDResourceName, &azmanagedidentity.FederatedIdentityCredentialArgs{
			ResourceName:                            identity.Name,
			FederatedIdentityCredentialResourceName: pulumi.StringPtr(fedIDResourceName),
			ResourceGroupName:                       pulumi.String(params.resourceGroupName),
			Subject:                                 pulumi.String(fmt.Sprintf("system:serviceaccount:%s:%s", helmExternalDNSNamespace, azureRoleExternalDNS)),
			Issuer:                                  pulumi.String(oidcIssuerURL),
			Audiences:                               pulumi.StringArray{pulumi.String("api://AzureADTokenExchange")},
		}, pulumi.Parent(identity),
			withAlias("azure-native:managedidentity:FederatedIdentityCredential", fedIDResourceName))
		if err != nil {
			return fmt.Errorf("helm azure: failed to create external-dns federated credential for %s: %w", release, err)
		}
	}

	// Kubernetes namespace.
	nsName := compoundName + "-" + release + "-external-dns-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmExternalDNSNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create external-dns namespace for %s: %w", release, err)
	}

	// azure.json config secret.
	azureConfig := map[string]interface{}{
		"tenantId":                     params.tenantID,
		"subscriptionId":               params.subscriptionID,
		"resourceGroup":                params.resourceGroupName,
		"useWorkloadIdentityExtension": true,
	}
	azureConfigJSON, jsonErr := json.Marshal(azureConfig)
	if jsonErr != nil {
		return fmt.Errorf("helm azure: failed to marshal azure.json for external-dns: %w", jsonErr)
	}
	azureConfigB64 := base64.StdEncoding.EncodeToString(azureConfigJSON)

	secretResourceName := compoundName + "-" + release + "-external-dns-secret"
	_, err = corev1.NewSecret(ctx, secretResourceName, &corev1.SecretArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("azure-config-file"),
			Namespace: pulumi.String(helmExternalDNSNamespace),
		},
		Data: pulumi.StringMap{
			"azure.json": pulumi.String(azureConfigB64),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Secret", secretResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create external-dns secret for %s: %w", release, err)
	}

	// Collect sorted domain filters from all sites.
	domainFilters := make([]interface{}, 0, len(params.cfg.Sites))
	for _, sn := range helpers.SortedKeys(params.cfg.Sites) {
		domainFilters = append(domainFilters, params.cfg.Sites[sn].Spec.Domain)
	}

	clusterName := compoundName + "-" + release

	// Build values using pulumi.Output since clientID is an output.
	chartResourceName := compoundName + "-" + release + "-external-dns-helm-release"
	valuesYAML := identity.ClientId.ApplyT(func(clientID string) (string, error) {
		v := map[string]interface{}{
			"provider":      "azure",
			"domainFilters": domainFilters,
			"extraArgs": map[string]interface{}{
				"txt-wildcard-replacement": "wildcard",
			},
			"extraVolumes": []interface{}{
				map[string]interface{}{
					"name": "azure-config-file",
					"secret": map[string]interface{}{
						"secretName": "azure-config-file",
					},
				},
			},
			"extraVolumeMounts": []interface{}{
				map[string]interface{}{
					"name":      "azure-config-file",
					"mountPath": "/etc/kubernetes",
					"readOnly":  true,
				},
			},
			"policy":     "sync",
			"txtOwnerId": clusterName,
			"txtPrefix":  "_d",
			"podLabels": map[string]interface{}{
				"azure.workload.identity/use": "true",
			},
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   azureRoleExternalDNS,
				"annotations": map[string]interface{}{
					"azure.workload.identity/client-id": clientID,
				},
				"labels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
			},
		}
		data, marshalErr := yaml.Marshal(v)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(data), nil
	}).(pulumi.StringOutput)

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("external-dns"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"repo":            pulumi.String("https://kubernetes-sigs.github.io/external-dns/"),
				"chart":           pulumi.String("external-dns"),
				"targetNamespace": pulumi.String(helmExternalDNSNamespace),
				"version":         pulumi.String(version),
				"valuesContent":   valuesYAML,
			},
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	return err
}

func azureHelmLoki(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params azureHelmParams, oidcIssuerURL, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	identity, err := azureHelmBlobStorageIdentity(ctx, compoundName, release, "loki",
		helmLokiNamespace, azureRoleLoki, params, oidcIssuerURL, withAlias)
	if err != nil {
		return err
	}

	nsName := compoundName + "-" + release + "-loki-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmLokiNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create loki namespace for %s: %w", release, err)
	}

	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)
	chartResourceName := compoundName + "-" + release + "-loki-helm-release"

	valuesYAML := identity.ClientId.ApplyT(func(clientID string) (string, error) {
		lokiCfg := map[string]interface{}{
			"auth_enabled": false,
			"podLabels": map[string]interface{}{
				"azure.workload.identity/use": "true",
			},
			"compactor": map[string]interface{}{
				"retention_enabled":    true,
				"delete_request_store": "azure",
			},
			"limits_config": map[string]interface{}{
				"retention_period": "30d",
			},
			"schemaConfig": map[string]interface{}{
				"configs": []interface{}{
					map[string]interface{}{
						"store":        "tsdb",
						"object_store": "azure",
						"schema":       "v13",
						"index": map[string]interface{}{
							"prefix": "loki_index_",
							"period": "24h",
						},
					},
				},
			},
			"storage_config": map[string]interface{}{
				"azure": map[string]interface{}{
					"account_name":        params.storageAccountName,
					"container_name":      "loki",
					"use_federated_token": true,
				},
			},
			"storage": map[string]interface{}{
				"type": "azure",
				"bucketNames": map[string]interface{}{
					"chunks": "loki",
				},
				"azure": map[string]interface{}{
					"accountName":       params.storageAccountName,
					"useFederatedToken": true,
				},
			},
		}
		if !thirdParty {
			lokiCfg["analytics"] = map[string]interface{}{"reporting_enabled": false}
		}

		v := map[string]interface{}{
			"gateway": map[string]interface{}{
				"image": map[string]interface{}{
					"registry":   "quay.io",
					"repository": "nginx/nginx-unprivileged",
				},
			},
			"loki": lokiCfg,
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   azureRoleLoki,
				"annotations": map[string]interface{}{
					"azure.workload.identity/client-id": clientID,
				},
				"labels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
			},
			"sidecar": map[string]interface{}{
				"image": map[string]interface{}{
					"repository": "quay.io/kiwigrid/k8s-sidecar",
				},
			},
			"monitoring": map[string]interface{}{
				"dashboards":     map[string]interface{}{"enabled": false},
				"serviceMonitor": map[string]interface{}{"enabled": false},
				"selfMonitoring": map[string]interface{}{
					"enabled":      false,
					"grafanaAgent": map[string]interface{}{"installOperator": false},
				},
			},
			"test": map[string]interface{}{"enabled": false},
		}
		data, marshalErr := yaml.Marshal(v)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(data), nil
	}).(pulumi.StringOutput)

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("loki"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"repo":            pulumi.String("https://grafana.github.io/helm-charts"),
				"chart":           pulumi.String("loki"),
				"targetNamespace": pulumi.String(helmLokiNamespace),
				"version":         pulumi.String(version),
				"valuesContent":   valuesYAML,
			},
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	return err
}

func azureHelmMimir(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params azureHelmParams, oidcIssuerURL, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	identity, err := azureHelmBlobStorageIdentity(ctx, compoundName, release, "mimir",
		helmMimirNamespace, azureRoleMimir, params, oidcIssuerURL, withAlias)
	if err != nil {
		return err
	}

	nsName := compoundName + "-" + release + "-mimir-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmMimirNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create mimir namespace for %s: %w", release, err)
	}

	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)
	chartResourceName := compoundName + "-" + release + "-mimir-helm-release"

	valuesYAML := identity.ClientId.ApplyT(func(clientID string) (string, error) {
		structuredConfig := map[string]interface{}{
			"common": map[string]interface{}{
				"storage": map[string]interface{}{
					"backend": "azure",
					"azure": map[string]interface{}{
						"account_name": params.storageAccountName,
					},
				},
			},
			"blocks_storage": map[string]interface{}{
				"backend": "azure",
				"azure": map[string]interface{}{
					"container_name": "mimir-blocks",
				},
			},
			"limits": map[string]interface{}{
				"max_global_series_per_user": 800000,
				"max_label_names_per_series": 45,
			},
		}
		if !thirdParty {
			structuredConfig["usage_stats"] = map[string]interface{}{"enabled": false}
		}

		v := map[string]interface{}{
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   azureRoleMimir,
				"annotations": map[string]interface{}{
					"azure.workload.identity/client-id": clientID,
				},
				"labels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
			},
			"global": map[string]interface{}{
				"podLabels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
			},
			"mimir": map[string]interface{}{
				"structuredConfig": structuredConfig,
			},
			"minio":         map[string]interface{}{"enabled": false},
			"alertmanager":  map[string]interface{}{"enabled": false},
			"ruler":         map[string]interface{}{"enabled": false},
			"ingester":      map[string]interface{}{"persistentVolume": map[string]interface{}{"size": "20Gi"}},
			"compactor":     map[string]interface{}{"persistentVolume": map[string]interface{}{"size": "20Gi"}},
			"store_gateway": map[string]interface{}{"persistentVolume": map[string]interface{}{"size": "20Gi"}},
			"gateway": map[string]interface{}{
				"enabledNonEnterprise": true,
				"nginx": map[string]interface{}{
					"image": map[string]interface{}{
						"registry":   "quay.io",
						"repository": "nginx/nginx-unprivileged",
					},
				},
			},
			"nginx": map[string]interface{}{"enabled": false},
		}
		data, marshalErr := yaml.Marshal(v)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(data), nil
	}).(pulumi.StringOutput)

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("mimir"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"repo":            pulumi.String("https://grafana.github.io/helm-charts"),
				"chart":           pulumi.String("mimir-distributed"),
				"targetNamespace": pulumi.String(helmMimirNamespace),
				"version":         pulumi.String(version),
				"valuesContent":   valuesYAML,
			},
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	return err
}

func azureHelmGrafana(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params azureHelmParams, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	domain := mainDomain(params.cfg.Sites)
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)

	// Grafana namespace.
	nsName := compoundName + "-" + release + "-grafana-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmGrafanaNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create grafana namespace for %s: %w", release, err)
	}

	// grafana-db-url secret from pre-fetched DB URL.
	dbURL := params.grafanaDBURLByCluster[release]
	if dbURL != "" {
		dbURLB64 := base64.StdEncoding.EncodeToString([]byte(dbURL))
		dbSecretResourceName := compoundName + "-" + release + "-grafana-db-url"
		_, err = corev1.NewSecret(ctx, dbSecretResourceName, &corev1.SecretArgs{
			Metadata: metav1.ObjectMetaArgs{
				Name:      pulumi.String("grafana-db-url"),
				Namespace: pulumi.String(helmGrafanaNamespace),
			},
			Data: pulumi.StringMap{
				"PTD_DATABASE_URL": pulumi.String(dbURLB64),
			},
		}, k8sOpt, withAlias("kubernetes:core/v1:Secret", dbSecretResourceName),
			pulumi.DependsOn([]pulumi.Resource{ns}))
		if err != nil {
			return fmt.Errorf("helm azure: failed to create grafana db secret for %s: %w", release, err)
		}
	}

	// Azure Grafana does not use auth.proxy (no X-Forwarded-User header injection);
	// authentication is handled upstream by Azure workload identity / Traefik forward auth.
	iniCfg := map[string]interface{}{
		"server": map[string]interface{}{
			"domain":              domain,
			"root_url":            fmt.Sprintf("https://grafana.%s", domain),
			"serve_from_sub_path": false,
		},
		"database": map[string]interface{}{
			"url":      "${PTD_DATABASE_URL}",
			"ssl_mode": "require",
		},
	}
	if !thirdParty {
		iniCfg["analytics"] = map[string]interface{}{
			"reporting_enabled": false,
			"check_for_updates": false,
		}
		iniCfg["plugins"] = map[string]interface{}{
			"check_for_plugin_updates": false,
		}
	}

	grafanaValues := map[string]interface{}{
		"envFromSecret": "grafana-db-url",
		"grafana.ini":   iniCfg,
		"ingress": map[string]interface{}{
			"enabled": true,
			"annotations": map[string]interface{}{
				"traefik.ingress.kubernetes.io/router.middlewares": "kube-system-traefik-forward-auth-add-forwarded-headers@kubernetescrd,kube-system-traefik-forward-auth-main@kubernetescrd",
			},
			"hosts": []interface{}{fmt.Sprintf("grafana.%s", domain)},
			"path":  "/",
		},
		"datasources": map[string]interface{}{
			"datasources.yaml": map[string]interface{}{
				"apiVersion": 1,
				"datasources": []interface{}{
					map[string]interface{}{
						"name":      "Loki",
						"type":      "loki",
						"access":    "proxy",
						"editable":  false,
						"url":       "http://loki-gateway.loki.svc.cluster.local",
						"isDefault": true,
					},
					map[string]interface{}{
						"name":      "Mimir",
						"type":      "prometheus",
						"access":    "proxy",
						"editable":  false,
						"url":       "http://mimir-gateway.mimir.svc.cluster.local/prometheus",
						"isDefault": false,
					},
				},
			},
		},
	}

	chartResourceName := compoundName + "-" + release + "-grafana-helm-release"
	return helmChartCR(ctx, chartResourceName, "grafana",
		clustersHelmControllerNamespace,
		"https://grafana.github.io/helm-charts",
		"grafana",
		helmGrafanaNamespace,
		version, grafanaValues, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
}

func azureHelmAlloy(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release string,
	params azureHelmParams, oidcIssuerURL, version string,
	withAlias func(string, string) pulumi.ResourceOption,
	withNestedAlias func(string, string, string) pulumi.ResourceOption) error {

	identity, err := azureHelmAlloyMonitoringIdentity(ctx, compoundName, release, params, oidcIssuerURL, withAlias)
	if err != nil {
		return err
	}

	nsName := compoundName + "-" + release + "-alloy-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmAlloyNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create alloy namespace for %s: %w", release, err)
	}

	// Build Alloy River config.
	domain := mainDomain(params.cfg.Sites)
	clusterName := compoundName + "-" + release
	clusterResourceGroupName := fmt.Sprintf("MC_%s_%s_%s", params.resourceGroupName, clusterName, params.region)

	alloyP := alloyConfigParams{
		compoundName:               compoundName,
		trueName:                   params.trueName,
		domain:                     domain,
		controlRoomDomain:          params.cfg.ControlRoomDomain,
		thirdPartyTelemetryEnabled: isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled),
		release:                    release,
		region:                     params.region,
		clusterName:                clusterName,
		accountIDOrTenantID:        params.tenantID,
		cloudProvider:              "azure",
		shouldScrapeSystemLogs:     true, // Azure always scrapes system logs (varlog: true)
		sites:                      params.cfg.Sites,
		workloadDir:                params.workloadDir,
		subscriptionID:             params.subscriptionID,
		resourceGroupName:          params.resourceGroupName,
		clusterResourceGroupName:   clusterResourceGroupName,
		publicSubnetCidr:           params.cfg.Network.PublicSubnetCidr,
	}
	alloyConfigStr := buildAlloyConfig(alloyP)

	// ConfigMap for Alloy. Python named the AlloyConfig component "alloy-config" and the ConfigMap
	// was a child of that component, so its URN uses the nested AlloyConfig parent type.
	cmResourceName := compoundName + "-" + release + "-alloy-config-configmap"
	_, err = corev1.NewConfigMap(ctx, cmResourceName, &corev1.ConfigMapArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("alloy-config"),
			Namespace: pulumi.String(helmAlloyNamespace),
		},
		Data: pulumi.StringMap{
			"config.alloy": pulumi.String(alloyConfigStr),
		},
	}, k8sOpt, withNestedAlias("ptd:AlloyConfig", "kubernetes:core/v1:ConfigMap", "alloy-config-configmap"),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create alloy configmap for %s: %w", release, err)
	}

	// Mimir auth Secret (Azure uses base64-encoded data field unlike AWS which uses stringData).
	mimirAuthB64 := base64.StdEncoding.EncodeToString([]byte(params.mimirPassword))
	mimirSecretResourceName := compoundName + "-" + release + "-mimir-auth"
	_, err = corev1.NewSecret(ctx, mimirSecretResourceName, &corev1.SecretArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("mimir-auth"),
			Namespace: pulumi.String(helmAlloyNamespace),
		},
		Data: pulumi.StringMap{
			"password": pulumi.String(mimirAuthB64),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Secret", mimirSecretResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	if err != nil {
		return fmt.Errorf("helm azure: failed to create alloy mimir-auth secret for %s: %w", release, err)
	}

	// Alloy Helm chart values (uses identity.ClientId output).
	thirdParty := isThirdPartyTelemetryEnabled(params.cfg.ThirdPartyTelemetryEnabled)
	chartResourceName := compoundName + "-" + release + "-grafana-alloy-release"

	valuesYAML := identity.ClientId.ApplyT(func(clientID string) (string, error) {
		alloyInner := map[string]interface{}{
			"clustering": map[string]interface{}{"enabled": true},
			"extraPorts": []interface{}{
				map[string]interface{}{
					"name":       "faro",
					"port":       12347,
					"targetPort": 12347,
					"protocol":   "TCP",
				},
			},
			"mounts": map[string]interface{}{
				"extra": []interface{}{
					map[string]interface{}{
						"name":      "mimir-auth",
						"mountPath": "/etc/mimir/",
						"readOnly":  true,
					},
				},
				"varlog": true,
			},
			"configMap": map[string]interface{}{
				"create": false,
				"name":   "alloy-config",
				"key":    "config.alloy",
			},
		}
		if !thirdParty {
			alloyInner["reporting"] = map[string]interface{}{"enabled": false}
		}

		v := map[string]interface{}{
			"serviceAccount": map[string]interface{}{
				"create": true,
				"name":   azureRoleAlloy,
				"annotations": map[string]interface{}{
					"azure.workload.identity/client-id": clientID,
				},
				"labels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
			},
			"controller": map[string]interface{}{
				"podLabels": map[string]interface{}{
					"azure.workload.identity/use": "true",
				},
				"volumes": map[string]interface{}{
					"extra": []interface{}{
						map[string]interface{}{
							"name": "mimir-auth",
							"secret": map[string]interface{}{
								"secretName": "mimir-auth",
								"items": []interface{}{
									map[string]interface{}{
										"key":  "password",
										"path": "password",
									},
								},
							},
						},
					},
				},
			},
			"alloy": alloyInner,
			"ingress": map[string]interface{}{
				"enabled":  true,
				"faroPort": 12347,
				"hosts":    []interface{}{fmt.Sprintf("faro.%s", domain)},
			},
		}
		data, marshalErr := yaml.Marshal(v)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(data), nil
	}).(pulumi.StringOutput)

	_, err = apiextensions.NewCustomResource(ctx, chartResourceName, &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("helm.cattle.io/v1"),
		Kind:       pulumi.String("HelmChart"),
		Metadata: metav1.ObjectMetaArgs{
			Name:      pulumi.String("alloy"),
			Namespace: pulumi.String(clustersHelmControllerNamespace),
		},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"repo":            pulumi.String("https://grafana.github.io/helm-charts"),
				"chart":           pulumi.String("alloy"),
				"targetNamespace": pulumi.String(helmAlloyNamespace),
				"version":         pulumi.String(version),
				"valuesContent":   valuesYAML,
			},
		},
	}, k8sOpt, withAlias("kubernetes:helm.cattle.io/v1:HelmChart", chartResourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
	return err
}

func azureHelmKubeStateMetrics(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	values := map[string]interface{}{
		"metricLabelsAllowlist": []interface{}{"pods=[launcher-instance-id,user-group-*]"},
	}
	resourceName := compoundName + "-" + release + "-kube-state-metrics-helm-release"
	return helmChartCR(ctx, resourceName, "kube-state-metrics",
		clustersHelmControllerNamespace,
		"https://prometheus-community.github.io/helm-charts",
		"kube-state-metrics",
		clustersKubeSystemNamespace,
		version, values, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName))
}

func azureHelmNvidiaDevicePlugin(ctx *pulumi.Context, k8sOpt pulumi.ResourceOption, compoundName, release, version string,
	withAlias func(string, string) pulumi.ResourceOption) error {

	nsName := compoundName + "-" + release + "-nvidia-device-plugin-ns"
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: metav1.ObjectMetaArgs{
			Name: pulumi.String(helmNvidiaNamespace),
		},
	}, k8sOpt, withAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return err
	}

	resourceName := compoundName + "-" + release + "-nvidia-device-plugin-helm-release"
	return helmChartCR(ctx, resourceName, "nvidia-device-plugin",
		clustersHelmControllerNamespace,
		"https://nvidia.github.io/k8s-device-plugin",
		"nvidia-device-plugin",
		helmNvidiaNamespace,
		version, map[string]interface{}{}, k8sOpt,
		withAlias("kubernetes:helm.cattle.io/v1:HelmChart", resourceName),
		pulumi.DependsOn([]pulumi.Resource{ns}))
}
