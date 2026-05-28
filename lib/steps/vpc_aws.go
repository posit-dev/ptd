package steps

// awsVpc is a Go port of the Python AWSVpc ComponentResource.
// It encapsulates VPC, subnets, route tables, NACLs, and optional addons
// (NAT gateways, flow logs, VPC endpoints, extra NACL rules, secure defaults).
//
// Unlike the Python version this is NOT a Pulumi ComponentResource — all
// resources are created as direct children of the root stack.  Aliases bridge
// from the old Python parent URNs.

import (
	"fmt"
	"net"
	"strings"

	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// awsVpcState holds all the Pulumi resource handles that are produced when
// building a VPC.  Builder methods attach additional resources and return the
// same pointer so callers can chain calls fluently.
type awsVpcState struct {
	ctx  *pulumi.Context
	name string
	tags pulumi.StringMap

	// cidrBlock is the parsed VPC CIDR (string form, e.g. "172.16.0.0/20").
	cidrBlock string

	// subnetCIDRs holds the pre-computed public and private subnet CIDRs.
	publicSubnetCIDRs  []string
	privateSubnetCIDRs []string

	// Core resources
	vpc            *awsec2.Vpc
	publicSubnets  []*awsec2.Subnet
	privateSubnets []*awsec2.Subnet

	publicRouteTable   *awsec2.RouteTable
	privateRouteTables []*awsec2.RouteTable

	publicNACL  *awsec2.NetworkAcl
	privateNACL *awsec2.NetworkAcl

	vpcEndpointSG *awsec2.SecurityGroup

	// NACL rule counters (incremented by withNACLRule)
	nextPublicIngressRule  int
	nextPublicEgressRule   int
	nextPrivateIngressRule int
	nextPrivateEgressRule  int

	// aliasFunc returns a pulumi.ResourceOption that adds an alias pointing to
	// the old Python URN for this resource.  It is set once during construction
	// and reused by all builder methods.
	aliasFunc func(resourceType, resourceName string) pulumi.ResourceOption

	// providerResource is the underlying Pulumi ProviderResource (if any).
	// Use providerOpt() to get a ResourceOrInvokeOption suitable for both
	// resource creation and data-source lookup calls.
	providerResource pulumi.ProviderResource
}

// appendProvider appends the configured provider as a ResourceOption to opts
// (if one was configured) and returns the result.
func (v *awsVpcState) appendProvider(opts []pulumi.ResourceOption) []pulumi.ResourceOption {
	if v.providerResource != nil {
		opts = append(opts, pulumi.Provider(v.providerResource))
	}
	return opts
}

// invokeProvider returns a pulumi.InvokeOption for the configured provider,
// used for data-source lookup functions (e.g. LookupVpcEndpointService).
// Returns nil if no provider was configured.
func (v *awsVpcState) invokeProvider() pulumi.InvokeOption {
	if v.providerResource == nil {
		return nil
	}
	return pulumi.Provider(v.providerResource)
}

// computeSubnetCIDRs replicates ptd.SubnetCIDRBlocks.from_cidr_block().
//
// Given a VPC CIDR (must be at least /20), split into 4 equal sub-networks
// using the next-smaller prefix length (+2 bits).
//
//	top[0..2] → private subnets (one per AZ)
//	top[3]    → split again (+2 bits):
//	  sub[0..2] → public subnets
//	  sub[3]    → (managed, unused here)
func computeSubnetCIDRs(cidr string, azCount int) (public []string, private []string, err error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}

	ones, bits := network.Mask.Size()
	if bits-ones < 2 {
		return nil, nil, fmt.Errorf("CIDR %q is too small to subdivide", cidr)
	}

	topSubnets, err := subnetSplit(network, ones+2)
	if err != nil {
		return nil, nil, err
	}
	// First 3 top subnets are private (we take only azCount of them)
	for i := 0; i < azCount && i < 3; i++ {
		private = append(private, topSubnets[i].String())
	}

	// Fourth top subnet is split again for public
	subSubnets, err := subnetSplit(topSubnets[3], ones+4)
	if err != nil {
		return nil, nil, err
	}
	for i := 0; i < azCount && i < 3; i++ {
		public = append(public, subSubnets[i].String())
	}
	return public, private, nil
}

// subnetSplit splits a network into 4 equal sub-networks using newBits bits
// (absolute prefix length).
func subnetSplit(network *net.IPNet, newPrefix int) ([]*net.IPNet, error) {
	ones, bits := network.Mask.Size()
	if newPrefix <= ones || newPrefix > bits {
		return nil, fmt.Errorf("cannot split /%d network into /%d subnets", ones, newPrefix)
	}
	subnets := make([]*net.IPNet, 1<<uint(newPrefix-ones))
	ip := cloneIP(network.IP)
	mask := net.CIDRMask(newPrefix, bits)
	size := subnetSize(newPrefix, bits)
	for i := range subnets {
		subnets[i] = &net.IPNet{IP: cloneIP(ip), Mask: mask}
		addToIP(ip, size)
	}
	return subnets, nil
}

func cloneIP(ip net.IP) net.IP {
	cp := make(net.IP, len(ip))
	copy(cp, ip)
	return cp
}

func subnetSize(prefix, bits int) int {
	return 1 << uint(bits-prefix)
}

func addToIP(ip net.IP, n int) {
	for i := len(ip) - 1; i >= 0 && n > 0; i-- {
		sum := int(ip[i]) + n
		ip[i] = byte(sum & 0xff)
		n = sum >> 8
	}
}

// newAWSVpc creates the core VPC infrastructure resources and returns an
// *awsVpcState from which builder methods can be called.
//
// Parameters:
//
//	ctx           – Pulumi context
//	name          – logical VPC name (used as Pulumi resource name prefix)
//	cidr          – VPC CIDR block
//	azs           – list of AZ IDs (e.g. ["use1-az4", "use1-az6"])
//	tags          – resource tags (applied to every resource)
//	networkTags   – extra tags per privacy tier ("public"/"private")
//	outerCompType – Pulumi type string of the Python parent component
//	                (e.g. "ptd:AWSControlRoomWorkspaces$ptd:AWSVpc")
//	projectName   – the OLD Python Pulumi project name, used verbatim in alias
//	                URNs. MUST be the literal old project string (NOT
//	                ctx.Project()); the migration playbook forbids ctx.Project()
//	                in alias URNs.
//	provider      – optional provider resource option (nil → use stack default)
func newAWSVpc(
	ctx *pulumi.Context,
	name string,
	cidr string,
	azs []string,
	tags map[string]string,
	networkTags map[string]map[string]string, // "public"|"private" → extra tags
	outerCompType string, // used to build alias URNs
	projectName string, // OLD Python project name for alias URNs (literal, not ctx.Project())
	provider pulumi.ProviderResource, // optional; nil → use stack default
) (*awsVpcState, error) {
	publicCIDRs, privateCIDRs, err := computeSubnetCIDRs(cidr, len(azs))
	if err != nil {
		return nil, fmt.Errorf("newAWSVpc %q: %w", name, err)
	}

	v := &awsVpcState{
		ctx:                    ctx,
		name:                   name,
		cidrBlock:              cidr,
		publicSubnetCIDRs:      publicCIDRs,
		privateSubnetCIDRs:     privateCIDRs,
		nextPublicIngressRule:  2000,
		nextPublicEgressRule:   2000,
		nextPrivateIngressRule: 2000,
		nextPrivateEgressRule:  2000,
		providerResource:       provider,
	}

	// Build the tags map for Pulumi (merge base + Name).
	baseTags := pulumi.StringMap{}
	for k, val := range tags {
		baseTags[k] = pulumi.String(val)
	}
	v.tags = baseTags

	// aliasFunc builds a single alias pointing to the old Python URN:
	//   urn:pulumi:{stack}::{project}::{outerCompType}${resourceType}::{resourceName}
	//
	// projectName is the OLD Python project name passed by the caller. We use it
	// verbatim rather than ctx.Project(): the migration playbook requires alias
	// URNs to reference the literal old Python project name so the intent is
	// explicit and immune to project-name drift.
	v.aliasFunc = func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), projectName,
			outerCompType,
			resourceType,
			resourceName,
		)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// Helper: merge base tags with "Name" and optional extra tags.
	mergeTags := func(extras ...map[string]string) pulumi.StringMap {
		merged := pulumi.StringMap{}
		for k, val := range tags {
			merged[k] = pulumi.String(val)
		}
		for _, extra := range extras {
			for k, val := range extra {
				merged[k] = pulumi.String(val)
			}
		}
		return merged
	}

	pubExtraTags := map[string]string{}
	privExtraTags := map[string]string{}
	if networkTags != nil {
		if pt, ok := networkTags["public"]; ok {
			pubExtraTags = pt
		}
		if pt, ok := networkTags["private"]; ok {
			privExtraTags = pt
		}
	}

	// --- VPC ---
	provOpts := v.appendProvider([]pulumi.ResourceOption{v.aliasFunc("aws:ec2/vpc:Vpc", name)})
	v.vpc, err = awsec2.NewVpc(ctx, name, &awsec2.VpcArgs{
		CidrBlock:          pulumi.String(cidr),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               mergeTags(map[string]string{"Name": name}),
	}, provOpts...)
	if err != nil {
		return nil, fmt.Errorf("VPC %q: %w", name, err)
	}

	// --- Internet Gateway ---
	igwName := name
	igwOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/internetGateway:InternetGateway", igwName),
	})
	ig, err := awsec2.NewInternetGateway(ctx, igwName, &awsec2.InternetGatewayArgs{
		VpcId: v.vpc.ID(),
		Tags:  mergeTags(pubExtraTags, map[string]string{"Name": igwName}),
	}, igwOpts...)
	if err != nil {
		return nil, fmt.Errorf("IGW %q: %w", igwName, err)
	}

	// --- Public Subnets ---
	for i, az := range azs {
		num := i + 1
		subnetName := fmt.Sprintf("%s-public-az%d", name, num)
		subOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.vpc),
			v.aliasFunc("aws:ec2/subnet:Subnet", subnetName),
		})
		sub, serr := awsec2.NewSubnet(ctx, subnetName, &awsec2.SubnetArgs{
			VpcId:               v.vpc.ID(),
			CidrBlock:           pulumi.String(publicCIDRs[i]),
			AvailabilityZoneId:  pulumi.String(az),
			MapPublicIpOnLaunch: pulumi.Bool(false),
			Tags:                mergeTags(pubExtraTags, map[string]string{"Name": subnetName}),
		}, subOpts...)
		if serr != nil {
			return nil, fmt.Errorf("public subnet %q: %w", subnetName, serr)
		}
		v.publicSubnets = append(v.publicSubnets, sub)
	}

	// --- Private Subnets ---
	for i, az := range azs {
		num := i + 1
		subnetName := fmt.Sprintf("%s-private-az%d", name, num)
		subOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.vpc),
			v.aliasFunc("aws:ec2/subnet:Subnet", subnetName),
		})
		sub, serr := awsec2.NewSubnet(ctx, subnetName, &awsec2.SubnetArgs{
			VpcId:               v.vpc.ID(),
			CidrBlock:           pulumi.String(privateCIDRs[i]),
			AvailabilityZoneId:  pulumi.String(az),
			MapPublicIpOnLaunch: pulumi.Bool(false),
			Tags:                mergeTags(privExtraTags, map[string]string{"Name": subnetName}),
		}, subOpts...)
		if serr != nil {
			return nil, fmt.Errorf("private subnet %q: %w", subnetName, serr)
		}
		v.privateSubnets = append(v.privateSubnets, sub)
	}

	// --- Public Route Table ---
	pubRTName := fmt.Sprintf("%s-public", name)
	pubRTOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/routeTable:RouteTable", pubRTName),
	})
	v.publicRouteTable, err = awsec2.NewRouteTable(ctx, pubRTName, &awsec2.RouteTableArgs{
		VpcId: v.vpc.ID(),
		Tags:  mergeTags(pubExtraTags, map[string]string{"Name": pubRTName}),
	}, pubRTOpts...)
	if err != nil {
		return nil, fmt.Errorf("public RT %q: %w", pubRTName, err)
	}

	// Default public route (IGW)
	pubRouteName := fmt.Sprintf("%s-public", name)
	pubRouteOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.publicRouteTable),
		v.aliasFunc("aws:ec2/route:Route", pubRouteName),
	})
	if _, err := awsec2.NewRoute(ctx, pubRouteName, &awsec2.RouteArgs{
		RouteTableId:         v.publicRouteTable.ID(),
		GatewayId:            ig.ID(),
		DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
	}, pubRouteOpts...); err != nil {
		return nil, fmt.Errorf("public default route: %w", err)
	}

	// Public route table associations
	for i, sub := range v.publicSubnets {
		num := i + 1
		assocName := fmt.Sprintf("%s-public-az%d", name, num)
		assocOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.publicRouteTable),
			v.aliasFunc("aws:ec2/routeTableAssociation:RouteTableAssociation", assocName),
		})
		if _, err := awsec2.NewRouteTableAssociation(ctx, assocName, &awsec2.RouteTableAssociationArgs{
			SubnetId:     sub.ID(),
			RouteTableId: v.publicRouteTable.ID(),
		}, assocOpts...); err != nil {
			return nil, fmt.Errorf("public RT assoc az%d: %w", num, err)
		}
	}

	// --- Private Route Tables (one per AZ) ---
	for i, sub := range v.privateSubnets {
		num := i + 1
		privRTName := fmt.Sprintf("%s-private-az%d", name, num)
		privRTOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.vpc),
			v.aliasFunc("aws:ec2/routeTable:RouteTable", privRTName),
		})
		rt, rterr := awsec2.NewRouteTable(ctx, privRTName, &awsec2.RouteTableArgs{
			VpcId: v.vpc.ID(),
			Tags:  mergeTags(privExtraTags, map[string]string{"Name": privRTName}),
		}, privRTOpts...)
		if rterr != nil {
			return nil, fmt.Errorf("private RT az%d: %w", num, rterr)
		}
		v.privateRouteTables = append(v.privateRouteTables, rt)

		assocName := fmt.Sprintf("%s-private-az%d", name, num)
		assocOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(rt),
			v.aliasFunc("aws:ec2/routeTableAssociation:RouteTableAssociation", assocName),
		})
		if _, err := awsec2.NewRouteTableAssociation(ctx, assocName, &awsec2.RouteTableAssociationArgs{
			SubnetId:     sub.ID(),
			RouteTableId: rt.ID(),
		}, assocOpts...); err != nil {
			return nil, fmt.Errorf("private RT assoc az%d: %w", num, err)
		}
	}

	// --- Public NACL ---
	pubNACLName := fmt.Sprintf("%s-public", name)
	pubNACLOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/networkAcl:NetworkAcl", pubNACLName),
	})
	pubSubnetIDs := make(pulumi.StringArray, len(v.publicSubnets))
	for i, s := range v.publicSubnets {
		pubSubnetIDs[i] = s.ID()
	}
	v.publicNACL, err = awsec2.NewNetworkAcl(ctx, pubNACLName, &awsec2.NetworkAclArgs{
		VpcId:     v.vpc.ID(),
		SubnetIds: pubSubnetIDs,
		Tags:      mergeTags(pubExtraTags, map[string]string{"Name": pubNACLName}),
	}, pubNACLOpts...)
	if err != nil {
		return nil, fmt.Errorf("public NACL: %w", err)
	}

	if err := v.createPublicNACLBaseRules(); err != nil {
		return nil, err
	}

	// --- Private NACL ---
	privNACLName := fmt.Sprintf("%s-private", name)
	privNACLOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/networkAcl:NetworkAcl", privNACLName),
	})
	privSubnetIDs := make(pulumi.StringArray, len(v.privateSubnets))
	for i, s := range v.privateSubnets {
		privSubnetIDs[i] = s.ID()
	}
	v.privateNACL, err = awsec2.NewNetworkAcl(ctx, privNACLName, &awsec2.NetworkAclArgs{
		VpcId:     v.vpc.ID(),
		SubnetIds: privSubnetIDs,
		Tags:      mergeTags(privExtraTags, map[string]string{"Name": privNACLName}),
	}, privNACLOpts...)
	if err != nil {
		return nil, fmt.Errorf("private NACL: %w", err)
	}

	if err := v.createPrivateNACLBaseRules(); err != nil {
		return nil, err
	}

	// --- VPC Endpoint Security Group ---
	sgName := fmt.Sprintf("%s-vpc-endpoint", name)
	sgOpts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/securityGroup:SecurityGroup", sgName),
	})
	v.vpcEndpointSG, err = awsec2.NewSecurityGroup(ctx, sgName, &awsec2.SecurityGroupArgs{
		Description: pulumi.Sprintf("%s VPC endpoint", name),
		NamePrefix:  pulumi.Sprintf("%s-vpc-endpoint-", name),
		Ingress: awsec2.SecurityGroupIngressArray{
			awsec2.SecurityGroupIngressArgs{
				FromPort:   pulumi.Int(443),
				ToPort:     pulumi.Int(443),
				Protocol:   pulumi.String("tcp"),
				CidrBlocks: pulumi.StringArray{pulumi.String(cidr)},
			},
		},
		VpcId: v.vpc.ID(),
		Tags:  mergeTags(map[string]string{"Name": sgName}),
	}, sgOpts...)
	if err != nil {
		return nil, fmt.Errorf("VPC endpoint SG: %w", err)
	}

	return v, nil
}

// naclRule creates a NetworkAclRule with the alias pointing to the Python URN.
func (v *awsVpcState) naclRule(
	resourceName string,
	nacl *awsec2.NetworkAcl,
	ruleNumber int,
	egress bool,
	protocol string, // numeric string: "6"=tcp, "17"=udp, "-1"=all
	fromPort, toPort int,
	cidr string,
	action string,
) error {
	opts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(nacl),
		v.aliasFunc("aws:ec2/networkAclRule:NetworkAclRule", resourceName),
		pulumi.DeleteBeforeReplace(true),
	})
	_, err := awsec2.NewNetworkAclRule(v.ctx, resourceName, &awsec2.NetworkAclRuleArgs{
		NetworkAclId: nacl.ID(),
		RuleNumber:   pulumi.Int(ruleNumber),
		Egress:       pulumi.Bool(egress),
		Protocol:     pulumi.String(protocol),
		FromPort:     pulumi.Int(fromPort),
		ToPort:       pulumi.Int(toPort),
		CidrBlock:    pulumi.String(cidr),
		RuleAction:   pulumi.String(action),
	}, opts...)
	return err
}

// createPublicNACLBaseRules creates the 6 base rules for the public NACL.
func (v *awsVpcState) createPublicNACLBaseRules() error {
	rules := []struct {
		name       string
		ruleNumber int
		egress     bool
		protocol   string
		from, to   int
		cidr       string
		action     string
	}{
		{fmt.Sprintf("%s-public-internal-https", v.name), 1000, false, "6", 443, 443, v.cidrBlock, "allow"},
		{fmt.Sprintf("%s-public-ssh-deny", v.name), 9000, false, "6", 22, 22, "0.0.0.0/0", "deny"},
		{fmt.Sprintf("%s-public-rdp-deny", v.name), 9001, false, "6", 3389, 3389, "0.0.0.0/0", "deny"},
		{fmt.Sprintf("%s-public-internal-ephemeral", v.name), 10000, false, "6", 1024, 65535, "0.0.0.0/0", "allow"},
		{fmt.Sprintf("%s-public-https-egress", v.name), 1000, true, "6", 443, 443, "0.0.0.0/0", "allow"},
		{fmt.Sprintf("%s-public-ephemeral-egress", v.name), 10000, true, "6", 1024, 65535, "0.0.0.0/0", "allow"},
	}
	for _, r := range rules {
		if err := v.naclRule(r.name, v.publicNACL, r.ruleNumber, r.egress, r.protocol, r.from, r.to, r.cidr, r.action); err != nil {
			return fmt.Errorf("public NACL rule %q: %w", r.name, err)
		}
	}
	return nil
}

// createPrivateNACLBaseRules creates the 6 base rules for the private NACL.
func (v *awsVpcState) createPrivateNACLBaseRules() error {
	rules := []struct {
		name       string
		ruleNumber int
		egress     bool
		protocol   string
		from, to   int
		cidr       string
		action     string
	}{
		{fmt.Sprintf("%s-private-ephemeral", v.name), 10000, false, "6", 1024, 65535, "0.0.0.0/0", "allow"},
		{fmt.Sprintf("%s-private-https-egress", v.name), 1000, true, "6", 443, 443, "0.0.0.0/0", "allow"},
		{fmt.Sprintf("%s-private-ssh-deny", v.name), 9000, false, "6", 22, 22, "0.0.0.0/0", "deny"},
		{fmt.Sprintf("%s-private-rdp-deny", v.name), 9001, false, "6", 3389, 3389, "0.0.0.0/0", "deny"},
		{fmt.Sprintf("%s-private-internal-ephemeral-egress", v.name), 10000, true, "6", 1024, 65535, v.cidrBlock, "allow"},
	}
	for _, r := range rules {
		if err := v.naclRule(r.name, v.privateNACL, r.ruleNumber, r.egress, r.protocol, r.from, r.to, r.cidr, r.action); err != nil {
			return fmt.Errorf("private NACL rule %q: %w", r.name, err)
		}
	}
	return nil
}

// withNACLRule adds a NACL rule to either the public or private NACL.
// This replicates AWSVpc.with_nacl_rule() from Python.
//
// The Python with_nacl_rule signature uses port_range and protocol="all"|"tcp"|"udp".
// protocol -1 means all; port=0 with protocol=-1 means all traffic.
func (v *awsVpcState) withNACLRule(
	privacy string, // "public" or "private"
	port int, // single port (0 for all-protocol)
	protocol int, // 6=tcp, 17=udp, -1=all
	cidr string,
	egress bool,
) error {
	var nacl *awsec2.NetworkAcl
	var ruleNum *int
	privacyStr := privacy
	if privacy == "public" {
		nacl = v.publicNACL
		if egress {
			ruleNum = &v.nextPublicEgressRule
		} else {
			ruleNum = &v.nextPublicIngressRule
		}
	} else {
		nacl = v.privateNACL
		if egress {
			ruleNum = &v.nextPrivateEgressRule
		} else {
			ruleNum = &v.nextPrivateIngressRule
		}
	}

	direction := "ingress"
	if egress {
		direction = "egress"
	}
	resourceName := fmt.Sprintf("%s-%s-%s-rule-%d", v.name, privacyStr, direction, *ruleNum)
	protoStr := fmt.Sprintf("%d", protocol)
	fromPort := port
	toPort := port
	if protocol == -1 {
		fromPort = 0
		toPort = 0
	}

	if err := v.naclRule(resourceName, nacl, *ruleNum, egress, protoStr, fromPort, toPort, cidr, "allow"); err != nil {
		return err
	}
	*ruleNum += 500
	return nil
}

// withSecureDefaultSecurityGroup locks down the default security group (EC2.2).
func (v *awsVpcState) withSecureDefaultSecurityGroup() error {
	resourceName := fmt.Sprintf("%s-default", v.name)
	opts := v.appendProvider([]pulumi.ResourceOption{
		// Python parent was the AWSVpc component resource
		v.aliasFunc("aws:ec2/defaultSecurityGroup:DefaultSecurityGroup", resourceName),
	})
	_, err := awsec2.NewDefaultSecurityGroup(v.ctx, resourceName, &awsec2.DefaultSecurityGroupArgs{
		VpcId: v.vpc.ID(),
	}, opts...)
	return err
}

// withSecureDefaultNACL locks down the default NACL (EC2.21).
func (v *awsVpcState) withSecureDefaultNACL() error {
	resourceName := fmt.Sprintf("%s-default", v.name)
	opts := v.appendProvider([]pulumi.ResourceOption{
		v.aliasFunc("aws:ec2/defaultNetworkAcl:DefaultNetworkAcl", resourceName),
	})
	_, err := awsec2.NewDefaultNetworkAcl(v.ctx, resourceName, &awsec2.DefaultNetworkAclArgs{
		DefaultNetworkAclId: v.vpc.DefaultNetworkAclId,
	}, opts...)
	return err
}

// withEndpoint creates a VPC endpoint for the given service name.
// Gateway type (s3, dynamodb) gets route table associations.
// Interface type gets private DNS and the vpc-endpoint SG.
func (v *awsVpcState) withEndpoint(service string) error {
	endpointType := "Gateway"
	if service != "s3" && service != "dynamodb" {
		endpointType = "Interface"
	}

	var lookupOpts []pulumi.InvokeOption
	if invokeOpt := v.invokeProvider(); invokeOpt != nil {
		lookupOpts = append(lookupOpts, invokeOpt)
	}
	svc, err := awsec2.LookupVpcEndpointService(v.ctx, &awsec2.LookupVpcEndpointServiceArgs{
		Service:     ref(service),
		ServiceType: ref(endpointType),
	}, lookupOpts...)
	if err != nil {
		return fmt.Errorf("lookup VPC endpoint service %q: %w", service, err)
	}

	endpointName := fmt.Sprintf("%s-%s", v.name, service)
	opts := v.appendProvider([]pulumi.ResourceOption{
		pulumi.Parent(v.vpc),
		v.aliasFunc("aws:ec2/vpcEndpoint:VpcEndpoint", endpointName),
	})

	args := &awsec2.VpcEndpointArgs{
		ServiceName:     pulumi.String(svc.ServiceName),
		VpcEndpointType: pulumi.String(svc.ServiceType),
		VpcId:           v.vpc.ID(),
		Tags:            v.tagsWithName(endpointName),
	}

	if strings.EqualFold(svc.ServiceType, "Gateway") {
		routeTableIDs := make(pulumi.StringArray, 0, len(v.privateRouteTables)+1)
		for _, rt := range v.privateRouteTables {
			routeTableIDs = append(routeTableIDs, rt.ID())
		}
		routeTableIDs = append(routeTableIDs, v.publicRouteTable.ID())
		args.RouteTableIds = routeTableIDs
	} else {
		args.PrivateDnsEnabled = pulumi.Bool(true)
		args.SecurityGroupIds = pulumi.StringArray{v.vpcEndpointSG.ID()}
		subnetIDs := make(pulumi.StringArray, len(v.publicSubnets))
		for i, s := range v.publicSubnets {
			subnetIDs[i] = s.ID()
		}
		args.SubnetIds = subnetIDs
	}

	_, err = awsec2.NewVpcEndpoint(v.ctx, endpointName, args, opts...)
	return err
}

// withNATGateways creates one EIP + NAT gateway per public subnet plus a
// default route in each private route table.
func (v *awsVpcState) withNATGateways() error {
	for i, sub := range v.publicSubnets {
		num := i + 1
		eipName := fmt.Sprintf("%s-az%d", v.name, num)
		eipOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.vpc),
			v.aliasFunc("aws:ec2/eip:Eip", eipName),
		})
		eip, err := awsec2.NewEip(v.ctx, eipName, &awsec2.EipArgs{
			Domain: pulumi.String("vpc"),
			Tags:   v.tagsWithName(eipName),
		}, eipOpts...)
		if err != nil {
			return fmt.Errorf("EIP az%d: %w", num, err)
		}

		ngwName := fmt.Sprintf("%s-az%d", v.name, num)
		ngwOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(v.vpc),
			v.aliasFunc("aws:ec2/natGateway:NatGateway", ngwName),
			pulumi.DeleteBeforeReplace(true),
		})
		ngw, err := awsec2.NewNatGateway(v.ctx, ngwName, &awsec2.NatGatewayArgs{
			SubnetId:     sub.ID(),
			AllocationId: eip.ID(),
			Tags:         v.tagsWithName(ngwName),
		}, ngwOpts...)
		if err != nil {
			return fmt.Errorf("NAT GW az%d: %w", num, err)
		}

		routeName := fmt.Sprintf("%s-nat-az%d", v.name, num)
		rt := v.privateRouteTables[i]
		routeOpts := v.appendProvider([]pulumi.ResourceOption{
			pulumi.Parent(rt),
			v.aliasFunc("aws:ec2/route:Route", routeName),
		})
		if _, err := awsec2.NewRoute(v.ctx, routeName, &awsec2.RouteArgs{
			RouteTableId:         rt.ID(),
			NatGatewayId:         ngw.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
		}, routeOpts...); err != nil {
			return fmt.Errorf("NAT route az%d: %w", num, err)
		}
	}
	return nil
}

// tagsWithName returns the base tags merged with a "Name" key.
func (v *awsVpcState) tagsWithName(name string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, val := range v.tags {
		out[k] = val
	}
	out["Name"] = pulumi.String(name)
	return out
}

// ref is a helper that returns a pointer to a string (for optional args).
func ref(s string) *string { return &s }
