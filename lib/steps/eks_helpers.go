package steps

import (
	"fmt"
	"regexp"
	"sort"

	awsalb "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/alb"
	awsroute53 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	kubernetes "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	apiextensions "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	helmv3 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// calicoResources builds a Kubernetes resources block following PTD's
// "memory-bounded, CPU-unbounded" policy for Calico components:
//   - Memory: request == limit. Memory is non-compressible, so equal request
//     and limit gives the pod a guaranteed, bounded allocation and prevents a
//     runaway component from exhausting node memory (which would crash the node).
//   - CPU: request only, no limit. Omitting the CPU limit avoids Linux CFS
//     throttling of the dataplane (a throttled calico-node degrades networking
//     cluster-wide).
//
// cpuRequest and memory are Kubernetes quantity strings (e.g. "250m", "512Mi").
func calicoResources(cpuRequest, memory string) pulumi.Map {
	return pulumi.Map{
		"requests": pulumi.Map{
			"cpu":    pulumi.String(cpuRequest),
			"memory": pulumi.String(memory),
		},
		"limits": pulumi.Map{
			"memory": pulumi.String(memory),
		},
	}
}

// calicoComponentOverride builds the operator component-deployment override that
// patches resources onto a single named container. The operator strategically
// merges this by container name into the rendered DaemonSet/Deployment, so only
// the name and resources need to be specified. Used under installation's
// calicoNodeDaemonSet / typhaDeployment / calicoKubeControllersDeployment and
// apiServer's apiServerDeployment.
func calicoComponentOverride(containerName, cpuRequest, memory string) pulumi.Map {
	return pulumi.Map{
		"spec": pulumi.Map{
			"template": pulumi.Map{
				"spec": pulumi.Map{
					"containers": pulumi.Array{
						pulumi.Map{
							"name":      pulumi.String(containerName),
							"resources": calicoResources(cpuRequest, memory),
						},
					},
				},
			},
		},
	}
}

// deployTigeraOperator ports python-pulumi/src/ptd/pulumi_resources/tigera_operator.py
// (the ptd:TigeraOperator nested ComponentResource created by
// aws_workload_eks.py:_define_tigera_operator). It installs the Calico/Tigera CNI:
// a tigera-operator namespace, a FelixConfiguration helm-adopt patch, the
// tigera-operator helm chart, and an Installation cni=Calico patch.
//
// The Python component was a child of AWSWorkloadEKS, so its children's old URN
// chain is "ptd:AWSWorkloadEKS$ptd:TigeraOperator$<type>::<name>". Every resource
// gets a full-URN alias bridging from that old parent chain (the Go resources are
// flat children of the stack).
//
// k8sProviderOpt selects the cluster's Kubernetes provider; name is the workload
// compound name; release the cluster release.
func deployTigeraOperator(
	ctx *pulumi.Context,
	name, release, version string,
	thirdPartyTelemetryEnabled bool,
	k8sProviderOpt pulumi.ResourceOption,
) error {
	// Old parent type chain for this component's children.
	const tigeraType = eksWorkloadWrapperType + "$ptd:TigeraOperator"
	tigeraAlias := func(resourceType, resourceName string) pulumi.ResourceOption {
		urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), eksOldProjectName, tigeraType, resourceType, resourceName)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
	}

	// ── Namespace ────────────────────────────────────────────────────────────
	nsName := fmt.Sprintf("%s-%s-tigera-ns", name, release)
	ns, err := corev1.NewNamespace(ctx, nsName, &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{Name: pulumi.String("tigera-operator")},
	}, k8sProviderOpt, tigeraAlias("kubernetes:core/v1:Namespace", nsName))
	if err != nil {
		return fmt.Errorf("eks: failed to create tigera namespace for %s: %w", release, err)
	}

	// ── FelixConfiguration helm-adopt patch ──────────────────────────────────
	// Adds Helm ownership metadata so the helm chart can adopt the default
	// FelixConfiguration the operator creates. ignoreChanges=["metadata"] matches
	// Python so the patch does not churn once applied.
	felixName := fmt.Sprintf("%s-%s-felix-helm-adopt", name, release)
	felixPatch, err := apiextensions.NewCustomResourcePatch(ctx, felixName, &apiextensions.CustomResourcePatchArgs{
		ApiVersion: pulumi.String("crd.projectcalico.org/v1"),
		Kind:       pulumi.String("FelixConfiguration"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:   pulumi.String("default"),
			Labels: pulumi.StringMap{"app.kubernetes.io/managed-by": pulumi.String("Helm")},
			Annotations: pulumi.StringMap{
				"meta.helm.sh/release-name":      pulumi.String("tigera-operator"),
				"meta.helm.sh/release-namespace": pulumi.String("tigera-operator"),
			},
		},
	}, k8sProviderOpt, pulumi.DependsOn([]pulumi.Resource{ns}), pulumi.IgnoreChanges([]string{"metadata"}),
		tigeraAlias("kubernetes:crd.projectcalico.org/v1:FelixConfigurationPatch", felixName))
	if err != nil {
		return fmt.Errorf("eks: failed to create felix helm-adopt patch for %s: %w", release, err)
	}

	// ── Tigera operator helm release ─────────────────────────────────────────
	felixCfg := pulumi.Map{"enabled": pulumi.Bool(true)}
	if !thirdPartyTelemetryEnabled {
		felixCfg["usageReportingEnabled"] = pulumi.Bool(false)
	}
	helmName := fmt.Sprintf("%s-%s-tigera-operator", name, release)
	helmRelease, err := helmv3.NewRelease(ctx, helmName, &helmv3.ReleaseArgs{
		Chart:     pulumi.String("tigera-operator"),
		Version:   pulumi.String(version),
		Namespace: pulumi.String("tigera-operator"),
		Name:      pulumi.String("tigera-operator"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{
			Repo: pulumi.String("https://docs.tigera.io/calico/charts"),
		},
		Atomic: pulumi.Bool(false),
		Values: pulumi.Map{
			"installation": pulumi.Map{
				"enabled":  pulumi.Bool(true),
				"registry": pulumi.String("quay.io"),
				"calicoNetwork": pulumi.Map{
					"bgp":       pulumi.String("Enabled"),
					"hostPorts": pulumi.String("Enabled"),
					"ipPools": pulumi.Array{
						pulumi.Map{
							"blockSize":     pulumi.Int(26),
							"cidr":          pulumi.String("172.16.0.0/16"),
							"encapsulation": pulumi.String("VXLAN"),
							"natOutgoing":   pulumi.String("Enabled"),
							"nodeSelector":  pulumi.String("all()"),
						},
					},
					"linuxDataplane":             pulumi.String("Iptables"),
					"multiInterfaceMode":         pulumi.String("None"),
					"nodeAddressAutodetectionV4": pulumi.Map{"firstFound": pulumi.Bool(true)},
				},
				"cni": pulumi.Map{
					"ipam": pulumi.Map{"type": pulumi.String("Calico")},
					"type": pulumi.String("Calico"),
				},
				// Resource overrides for the operator-managed dataplane
				// components (memory-bounded, CPU-unbounded — see calicoResources).
				// calico-node runs Felix, whose memory scales with the number of
				// endpoints and policies, so it gets the largest memory bound and
				// keeps the operator's built-in 250m CPU request floor.
				"calicoNodeDaemonSet":             calicoComponentOverride("calico-node", "250m", "512Mi"),
				"typhaDeployment":                 calicoComponentOverride("calico-typha", "100m", "256Mi"),
				"calicoKubeControllersDeployment": calicoComponentOverride("calico-kube-controllers", "50m", "128Mi"),
			},
			// Resources for the tigera/operator pod itself. Same
			// memory-bounded/CPU-unbounded policy as the dataplane components.
			"resources": calicoResources("100m", "256Mi"),
			// apiServer.enabled defaults to true in the chart; we merge in a
			// resource override for the calico-apiserver container.
			"apiServer": pulumi.Map{
				"apiServerDeployment": calicoComponentOverride("calico-apiserver", "100m", "256Mi"),
			},
			"goldmane":                  pulumi.Map{"enabled": pulumi.Bool(false)},
			"whisker":                   pulumi.Map{"enabled": pulumi.Bool(false)},
			"defaultFelixConfiguration": felixCfg,
		},
	}, k8sProviderOpt, pulumi.DependsOn([]pulumi.Resource{ns, felixPatch}),
		tigeraAlias("kubernetes:helm.sh/v3:Release", helmName))
	if err != nil {
		return fmt.Errorf("eks: failed to create tigera operator helm release for %s: %w", release, err)
	}

	// ── Installation cni=Calico patch ────────────────────────────────────────
	instName := fmt.Sprintf("%s-%s-installation-cni-patch", name, release)
	_, err = apiextensions.NewCustomResourcePatch(ctx, instName, &apiextensions.CustomResourcePatchArgs{
		ApiVersion: pulumi.String("operator.tigera.io/v1"),
		Kind:       pulumi.String("Installation"),
		Metadata:   &metav1.ObjectMetaArgs{Name: pulumi.String("default")},
		OtherFields: kubernetes.UntypedArgs{
			"spec": pulumi.Map{
				"cni": pulumi.Map{
					"type": pulumi.String("Calico"),
					"ipam": pulumi.Map{"type": pulumi.String("Calico")},
				},
			},
		},
	}, k8sProviderOpt, pulumi.DependsOn([]pulumi.Resource{helmRelease}),
		tigeraAlias("kubernetes:operator.tigera.io/v1:InstallationPatch", instName))
	if err != nil {
		return fmt.Errorf("eks: failed to create installation cni patch for %s: %w", release, err)
	}

	return nil
}

// ── Control-room Traefik (ptd:Traefik nested component) ──────────────────────

const clusterOldProjectName = "ptd-aws-control-room-cluster"
const clusterWrapperType = "ptd:AWSControlRoomCluster"

// controlRoomTraefik holds the deployed traefik release + the cluster context so
// the caller can invoke defineDomains after the cert/zone are known.
type controlRoomTraefik struct {
	release     *helmv3.Release
	ctx         *pulumi.Context
	clusterName string
	k8sProvider pulumi.ResourceOption
	namespace   string
}

// traefikAlias builds a full-URN alias for a child of the ptd:Traefik component.
// In Python the Traefik component was created with parent=self.eks (the
// AWSEKSCluster), itself a child of AWSControlRoomCluster, so the live old URN
// chain is ptd:AWSControlRoomCluster$ptd:AWSEKSCluster$ptd:Traefik$<type>::<name>.
// Omitting the $ptd:AWSEKSCluster segment generates a CREATE (orphaning the live
// traefik release + records) instead of adopting them.
func traefikAlias(ctx *pulumi.Context, resourceType, resourceName string) pulumi.ResourceOption {
	urn := fmt.Sprintf("urn:pulumi:%s::%s::%s$ptd:AWSEKSCluster$ptd:Traefik$%s::%s",
		ctx.Stack(), clusterOldProjectName, clusterWrapperType, resourceType, resourceName)
	return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(urn)}})
}

// deployControlRoomTraefik ports python-pulumi/src/ptd/pulumi_resources/traefik.py
// Traefik._deploy: the traefik helm release on a LoadBalancer service with NLB
// annotations (incl. the SSL cert arn + additional-resource-tags). Returns a
// handle for defineDomains. The Python component was a child of
// AWSControlRoomCluster, so children alias under ptd:AWSControlRoomCluster$ptd:Traefik.
func deployControlRoomTraefik(
	ctx *pulumi.Context,
	clusterName, namespace, version string,
	deploymentReplicas int,
	certARN pulumi.StringInput,
	trueName, environment string,
	k8sProvider pulumi.ResourceOption,
	protect bool,
) (*controlRoomTraefik, error) {
	nlbTags := formatLBTags(map[string]string{
		"posit.team/true-name":   trueName,
		"posit.team/environment": environment,
		"Name":                   clusterName,
	})

	values := pulumi.Map{
		"service": pulumi.Map{
			"type": pulumi.String("LoadBalancer"),
			"annotations": pulumi.Map{
				"service.beta.kubernetes.io/aws-load-balancer-type":                            pulumi.String("external"),
				"service.beta.kubernetes.io/aws-load-balancer-scheme":                          pulumi.String("internet-facing"),
				"service.beta.kubernetes.io/aws-load-balancer-ip-address-type":                 pulumi.String("ipv4"),
				"service.beta.kubernetes.io/aws-load-balancer-nlb-target-type":                 pulumi.String("ip"),
				"service.beta.kubernetes.io/aws-load-balancer-ssl-cert":                        certARN,
				"service.beta.kubernetes.io/aws-load-balancer-ssl-ports":                       pulumi.String("443"),
				"service.beta.kubernetes.io/aws-load-balancer-access-log-enabled":              pulumi.String("false"),
				"service.beta.kubernetes.io/aws-load-balancer-ssl-negotiation-policy":          pulumi.String("ELBSecurityPolicy-FS-1-2-2019-08"),
				"service.beta.kubernetes.io/aws-load-balancer-healthcheck-healthy-threshold":   pulumi.String("3"),
				"service.beta.kubernetes.io/aws-load-balancer-healthcheck-unhealthy-threshold": pulumi.String("3"),
				"service.beta.kubernetes.io/aws-load-balancer-healthcheck-timeout":             pulumi.String("10"),
				"service.beta.kubernetes.io/aws-load-balancer-healthcheck-interval":            pulumi.String("10"),
				"service.beta.kubernetes.io/aws-load-balancer-additional-resource-tags":        pulumi.String(nlbTags),
			},
		},
		"ports": pulumi.Map{
			"web":       pulumi.Map{"redirectTo": pulumi.String("websecure")},
			"websecure": pulumi.Map{"tls": pulumi.Map{"enabled": pulumi.Bool(false)}},
		},
		"providers": pulumi.Map{
			"kubernetesCRD":     pulumi.Map{"enabled": pulumi.Bool(true)},
			"kubernetesIngress": pulumi.Map{"enabled": pulumi.Bool(true)},
			"publishedService":  pulumi.Map{"enabled": pulumi.Bool(true)},
		},
		"additionalArguments": pulumi.Array{pulumi.String("--metrics.prometheus=true")},
		"resources": pulumi.Map{
			"requests": pulumi.Map{"cpu": pulumi.String("200m"), "memory": pulumi.String("256Mi")},
			"limits":   pulumi.Map{"cpu": pulumi.String("1000m"), "memory": pulumi.String("512Mi")},
		},
		"deployment":     pulumi.Map{"replicas": pulumi.Int(deploymentReplicas)},
		"livenessProbe":  pulumi.Map{"initialDelaySeconds": pulumi.Int(5), "periodSeconds": pulumi.Int(10), "timeoutSeconds": pulumi.Int(5), "failureThreshold": pulumi.Int(5)},
		"readinessProbe": pulumi.Map{"initialDelaySeconds": pulumi.Int(5), "periodSeconds": pulumi.Int(10), "timeoutSeconds": pulumi.Int(5), "failureThreshold": pulumi.Int(3)},
		"logs": pulumi.Map{
			"general": pulumi.Map{"level": pulumi.String("DEBUG")},
			"access":  pulumi.Map{"enabled": pulumi.Bool(true)},
		},
		"image":        pulumi.Map{"registry": pulumi.String("ghcr.io/traefik")},
		"ingressClass": pulumi.Map{"enabled": pulumi.Bool(true), "default": pulumi.Bool(true)},
		"ingressRoute": pulumi.Map{"dashboard": pulumi.Map{"enabled": pulumi.Bool(true)}},
	}

	opts := []pulumi.ResourceOption{
		k8sProvider,
		pulumi.DeleteBeforeReplace(true),
		traefikAlias(ctx, "kubernetes:helm.sh/v3:Release", clusterName+"-traefik"),
	}
	if protect {
		opts = append(opts, pulumi.Protect(true))
	}

	rel, err := helmv3.NewRelease(ctx, clusterName+"-traefik", &helmv3.ReleaseArgs{
		Chart:          pulumi.String("traefik"),
		Version:        pulumi.String(version),
		Namespace:      pulumi.String(namespace),
		Name:           pulumi.String("traefik"),
		RepositoryOpts: &helmv3.RepositoryOptsArgs{Repo: pulumi.String("https://helm.traefik.io/traefik/")},
		Values:         values,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("cluster: failed to create traefik helm release: %w", err)
	}

	return &controlRoomTraefik{
		release:     rel,
		ctx:         ctx,
		clusterName: clusterName,
		k8sProvider: k8sProvider,
		namespace:   namespace,
	}, nil
}

var albNamePrefixRE = regexp.MustCompile(`([a-zA-Z0-9-]*)-`)

// defineDomains ports Traefik.define_domains: looks up the traefik Service's NLB
// hostname, resolves the load balancer, and creates Route53 alias A-records (and
// CNAME records for any external/front-door domains) in the parent zone. The
// records are children of the ptd:Traefik component.
//
// domainsToCnames maps each apex/wildcard domain to its external CNAME target ("" = none).
func (t *controlRoomTraefik) defineDomains(zoneID pulumi.StringInput, domainsToCnames map[string]string) error {
	// Service.get the traefik service (depends on the release) to read its NLB
	// hostname (Python k8s.core.v1.Service.get("traefik", id="<ns>/<name>")).
	svc, err := corev1.GetService(t.ctx, "traefik",
		pulumi.ID(fmt.Sprintf("%s/traefik", t.namespace)), nil,
		t.k8sProvider, pulumi.DependsOn([]pulumi.Resource{t.release}))
	if err != nil {
		return fmt.Errorf("cluster: failed to get traefik service: %w", err)
	}

	// Resolve the ALB/NLB from the service status hostname (apply-time invoke).
	alb := svc.Status.ApplyT(func(status *corev1.ServiceStatus) (awsalb.LookupLoadBalancerResult, error) {
		hostname := ""
		if status != nil && status.LoadBalancer != nil && len(status.LoadBalancer.Ingress) > 0 && status.LoadBalancer.Ingress[0].Hostname != nil {
			hostname = *status.LoadBalancer.Ingress[0].Hostname
		}
		// Match all but the last dash-delimited term of the NLB hostname (AWS naming
		// convention), matching the Python regex "([a-zA-Z0-9-]*)-".
		name := ""
		if m := albNamePrefixRE.FindStringSubmatch(hostname); m != nil {
			name = m[1]
		}
		res, lerr := awsalb.LookupLoadBalancer(t.ctx, &awsalb.LookupLoadBalancerArgs{Name: &name}, nil)
		if lerr != nil {
			return awsalb.LookupLoadBalancerResult{}, lerr
		}
		return *res, nil
	}).(awsalb.LookupLoadBalancerResultOutput)

	albDNS := alb.ApplyT(func(r awsalb.LookupLoadBalancerResult) string { return r.DnsName }).(pulumi.StringOutput)
	albZone := alb.ApplyT(func(r awsalb.LookupLoadBalancerResult) string { return r.ZoneId }).(pulumi.StringOutput)

	domains := make([]string, 0, len(domainsToCnames))
	for d := range domainsToCnames {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	for _, domain := range domains {
		if _, err := awsroute53.NewRecord(t.ctx, fmt.Sprintf("%s-%s-A", t.clusterName, domain), &awsroute53.RecordArgs{
			ZoneId: zoneID,
			Name:   pulumi.String(domain),
			Type:   pulumi.String("A"),
			Aliases: awsroute53.RecordAliasArray{
				awsroute53.RecordAliasArgs{
					EvaluateTargetHealth: pulumi.Bool(true),
					Name:                 albDNS,
					ZoneId:               albZone,
				},
			},
		}, traefikAlias(t.ctx, "aws:route53/record:Record", fmt.Sprintf("%s-%s-A", t.clusterName, domain))); err != nil {
			return fmt.Errorf("cluster: failed to create A record for %s: %w", domain, err)
		}

		external := domainsToCnames[domain]
		if external != "" {
			if _, err := awsroute53.NewRecord(t.ctx, fmt.Sprintf("%s-%s-CNAME", t.clusterName, external), &awsroute53.RecordArgs{
				ZoneId:  zoneID,
				Name:    pulumi.String(external),
				Type:    pulumi.String("CNAME"),
				Records: pulumi.StringArray{pulumi.String(domain)},
				Ttl:     pulumi.Int(300),
			}, traefikAlias(t.ctx, "aws:route53/record:Record", fmt.Sprintf("%s-%s-CNAME", t.clusterName, external))); err != nil {
				return fmt.Errorf("cluster: failed to create CNAME record for %s: %w", external, err)
			}
		}
	}

	return nil
}
