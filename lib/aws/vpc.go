package aws

// AwsVpc is a Go port of the Python AWSVpc ComponentResource
// (python-pulumi/src/ptd/pulumi_resources/aws_vpc.py).
// It encapsulates VPC, subnets, route tables, NACLs, and optional addons
// (NAT gateways, flow logs, VPC endpoints, extra NACL rules, secure defaults).
//
// Unlike the Python version this is NOT a Pulumi ComponentResource — all
// resources are created as direct children of the root stack.  Aliases bridge
// from the old Python parent URNs.
//
// This builder is shared by the `workspaces` and `persistent` steps. It lives
// in lib/aws so both step files can construct it. The Pulumi *logical names*
// (first arg to every resource constructor) and the alias URN strings are
// identical to the original lib/steps/vpc_aws.go implementation so existing
// state (workspaces is applied to both control rooms) is not churned.

import (
	"fmt"
	"net"
	"strings"

	awscloudwatch "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	awsec2 "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	awsiam "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// AwsVpc holds all the Pulumi resource handles that are produced when
// building a VPC.  Builder methods attach additional resources and return the
// same pointer so callers can chain calls fluently.
type AwsVpc struct {
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

	// natGwPublicIps collects the NAT gateway public IPs created by
	// WithNATGateways (mirrors Python AWSVpc.nat_gw_public_ips). Exported via
	// NatGwPublicIps() for the control-room persistent step's outputs.
	natGwPublicIps []pulumi.StringOutput

	// Existing-VPC adoption mode (mirrors Python _init_with_existing_vpc).
	// When existingVPCID is non-empty, the builder did not create the VPC,
	// subnets, route tables, NACLs or NAT gateways; it adopted an existing VPC
	// and only created the vpc-endpoint security group. vpc/publicRouteTable are
	// nil and the private route tables / private subnet IDs are looked up.
	existingVPCID             string
	existingPrivateSubnetIDs  []string
	existingPrivateRouteTblID []string

	// NACL rule counters (incremented by WithNACLRule)
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

// Vpc returns the underlying VPC resource (nil in existing-VPC adoption mode).
func (v *AwsVpc) Vpc() *awsec2.Vpc { return v.vpc }

// VpcID returns the VPC ID as a string input, working in both greenfield and
// existing-VPC adoption modes.
func (v *AwsVpc) VpcID() pulumi.StringInput {
	if v.existingVPCID != "" {
		return pulumi.String(v.existingVPCID)
	}
	return v.vpc.ID()
}

// PublicSubnets returns the created public subnets (empty in adoption mode).
func (v *AwsVpc) PublicSubnets() []*awsec2.Subnet { return v.publicSubnets }

// PrivateSubnets returns the created private subnets (empty in adoption mode;
// private subnet IDs are available via PrivateSubnetIDs).
func (v *AwsVpc) PrivateSubnets() []*awsec2.Subnet { return v.privateSubnets }

// PrivateSubnetIDs returns the private subnet IDs as string inputs, working in
// both greenfield and existing-VPC adoption modes.
func (v *AwsVpc) PrivateSubnetIDs() pulumi.StringArray {
	if v.existingVPCID != "" {
		out := make(pulumi.StringArray, len(v.existingPrivateSubnetIDs))
		for i, id := range v.existingPrivateSubnetIDs {
			out[i] = pulumi.String(id)
		}
		return out
	}
	out := make(pulumi.StringArray, len(v.privateSubnets))
	for i, s := range v.privateSubnets {
		out[i] = s.ID()
	}
	return out
}

// VPCEndpointSG returns the VPC endpoint security group.
func (v *AwsVpc) VPCEndpointSG() *awsec2.SecurityGroup { return v.vpcEndpointSG }

// Name returns the logical VPC name (mirrors Python AWSVpc.name).
func (v *AwsVpc) Name() string { return v.name }

// NatGwPublicIps returns the NAT gateway public IPs as a StringArray, in the
// order the gateways were created (mirrors Python AWSVpc.nat_gw_public_ips).
func (v *AwsVpc) NatGwPublicIps() pulumi.StringArray {
	out := pulumi.StringArray{}
	for _, ip := range v.natGwPublicIps {
		out = append(out, ip)
	}
	return out
}

// PrivateRouteTableIDs returns the private route table IDs as string inputs,
// working in both greenfield (created route tables) and existing-VPC adoption
// (looked-up route table IDs) modes. Used by the persistent step's FSx OpenZFS
// MULTI_AZ_1 deployment, which routes the file system through the private RTs.
func (v *AwsVpc) PrivateRouteTableIDs() pulumi.StringArray {
	out := pulumi.StringArray{}
	for _, rt := range v.privateRouteTables {
		out = append(out, rt.ID())
	}
	for _, id := range v.existingPrivateRouteTblID {
		out = append(out, pulumi.String(id))
	}
	return out
}

// appendProvider appends the configured provider as a ResourceOption to opts
// (if one was configured) and returns the result.
func (v *AwsVpc) appendProvider(opts []pulumi.ResourceOption) []pulumi.ResourceOption {
	if v.providerResource != nil {
		opts = append(opts, pulumi.Provider(v.providerResource))
	}
	return opts
}

// invokeProvider returns a pulumi.InvokeOption for the configured provider,
// used for data-source lookup functions (e.g. LookupVpcEndpointService).
// Returns nil if no provider was configured.
func (v *AwsVpc) invokeProvider() pulumi.InvokeOption {
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

// subnetSplit splits a network into 4 equal sub-networks using newPrefix bits
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

// VPCConfig holds the construction parameters for NewVPC.
type VPCConfig struct {
	// Name is the logical VPC name (used as the Pulumi resource name prefix).
	Name string
	// CIDR is the VPC CIDR block.
	CIDR string
	// AZs is the list of AZ IDs (e.g. ["use1-az4", "use1-az6"]).
	AZs []string
	// Tags are resource tags applied to every resource.
	Tags map[string]string
	// NetworkTags are extra tags per privacy tier ("public"/"private").
	NetworkTags map[string]map[string]string
	// OuterCompType is the Pulumi type string of the Python parent component
	// (e.g. "ptd:AWSControlRoomWorkspaces$ptd:AWSVpc"), used to build alias URNs.
	OuterCompType string
	// ProjectName is the OLD Python Pulumi project name, used verbatim in alias
	// URNs. MUST be the literal old project string (NOT ctx.Project()); the
	// migration playbook forbids ctx.Project() in alias URNs.
	ProjectName string
	// Provider is an optional provider resource (nil → use stack default).
	Provider pulumi.ProviderResource

	// ExistingVPCID, when non-empty, switches the builder into existing-VPC
	// adoption mode (mirrors Python _init_with_existing_vpc): the VPC, subnets,
	// route tables, NACLs, IGW and NAT gateways are NOT created; the existing
	// VPC is adopted and only the vpc-endpoint security group is created. The
	// private route tables are looked up from ExistingPrivateSubnetIDs.
	ExistingVPCID string
	// ExistingPrivateSubnetIDs are the private subnet IDs of the adopted VPC.
	ExistingPrivateSubnetIDs []string
}

// NewVPC creates the core VPC infrastructure resources and returns an
// *AwsVpc from which builder methods can be called.
//
// When cfg.ExistingVPCID is set, the builder adopts an existing VPC instead of
// creating a new one (see VPCConfig.ExistingVPCID).
func NewVPC(ctx *pulumi.Context, cfg VPCConfig) (*AwsVpc, error) {
	v := &AwsVpc{
		ctx:                    ctx,
		name:                   cfg.Name,
		cidrBlock:              cfg.CIDR,
		nextPublicIngressRule:  2000,
		nextPublicEgressRule:   2000,
		nextPrivateIngressRule: 2000,
		nextPrivateEgressRule:  2000,
		providerResource:       cfg.Provider,
	}

	// Build the tags map for Pulumi.
	baseTags := pulumi.StringMap{}
	for k, val := range cfg.Tags {
		baseTags[k] = pulumi.String(val)
	}
	v.tags = baseTags

	// aliasFunc builds a single alias pointing to the old Python URN:
	//   urn:pulumi:{stack}::{project}::{outerCompType}${resourceType}::{resourceName}
	//
	// ProjectName is the OLD Python project name passed by the caller. We use it
	// verbatim rather than ctx.Project(): the migration playbook requires alias
	// URNs to reference the literal old Python project name so the intent is
	// explicit and immune to project-name drift.
	v.aliasFunc = func(resourceType, resourceName string) pulumi.ResourceOption {
		oldURN := fmt.Sprintf(
			"urn:pulumi:%s::%s::%s$%s::%s",
			ctx.Stack(), cfg.ProjectName,
			cfg.OuterCompType,
			resourceType,
			resourceName,
		)
		return pulumi.Aliases([]pulumi.Alias{{URN: pulumi.URN(oldURN)}})
	}

	// Existing-VPC adoption mode: do not build any networking, just adopt.
	if cfg.ExistingVPCID != "" {
		return v.initWithExistingVPC(cfg)
	}

	publicCIDRs, privateCIDRs, err := computeSubnetCIDRs(cfg.CIDR, len(cfg.AZs))
	if err != nil {
		return nil, fmt.Errorf("NewVPC %q: %w", cfg.Name, err)
	}
	v.publicSubnetCIDRs = publicCIDRs
	v.privateSubnetCIDRs = privateCIDRs

	name := cfg.Name
	cidr := cfg.CIDR

	// Helper: merge base tags with "Name" and optional extra tags.
	mergeTags := func(extras ...map[string]string) pulumi.StringMap {
		merged := pulumi.StringMap{}
		for k, val := range cfg.Tags {
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
	if cfg.NetworkTags != nil {
		if pt, ok := cfg.NetworkTags["public"]; ok {
			pubExtraTags = pt
		}
		if pt, ok := cfg.NetworkTags["private"]; ok {
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
	for i, az := range cfg.AZs {
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
	for i, az := range cfg.AZs {
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
	if err := v.createVPCEndpointSG(v.vpc.ID()); err != nil {
		return nil, err
	}

	return v, nil
}

// initWithExistingVPC mirrors Python AWSVpc._init_with_existing_vpc. It adopts
// an existing VPC: it does NOT create the VPC, subnets, route tables, NACLs,
// IGW or NAT gateways. It looks up the private route tables associated with the
// existing private subnets and creates the vpc-endpoint security group.
func (v *AwsVpc) initWithExistingVPC(cfg VPCConfig) (*AwsVpc, error) {
	v.existingVPCID = cfg.ExistingVPCID
	v.existingPrivateSubnetIDs = cfg.ExistingPrivateSubnetIDs

	// Look up route tables associated with the existing private subnets
	// (mirrors Python lookup_route_tables_for_subnets).
	rtIDs, err := v.lookupRouteTablesForSubnets(cfg.ExistingVPCID, cfg.ExistingPrivateSubnetIDs)
	if err != nil {
		return nil, fmt.Errorf("lookup route tables for existing subnets: %w", err)
	}
	v.existingPrivateRouteTblID = rtIDs

	// Create VPC endpoint security group for endpoints (parented to the
	// existing VPC reference; in Go inline mode there is no Vpc resource to
	// parent to, so it is a direct child of the stack like the other adopted
	// resources).
	if err := v.createVPCEndpointSG(pulumi.String(cfg.ExistingVPCID)); err != nil {
		return nil, err
	}

	return v, nil
}

// lookupRouteTablesForSubnets replicates Python
// AWSVpc.lookup_route_tables_for_subnets: find the route tables in vpcID whose
// associations include any of the given subnet IDs.
func (v *AwsVpc) lookupRouteTablesForSubnets(vpcID string, subnetIDs []string) ([]string, error) {
	var lookupOpts []pulumi.InvokeOption
	if invokeOpt := v.invokeProvider(); invokeOpt != nil {
		lookupOpts = append(lookupOpts, invokeOpt)
	}

	subnetSet := map[string]bool{}
	for _, id := range subnetIDs {
		subnetSet[id] = true
	}

	tables, err := awsec2.GetRouteTables(v.ctx, &awsec2.GetRouteTablesArgs{
		VpcId: &vpcID,
	}, lookupOpts...)
	if err != nil {
		return nil, err
	}

	var routeTableIDs []string
	for _, tableID := range tables.Ids {
		id := tableID
		table, err := awsec2.LookupRouteTable(v.ctx, &awsec2.LookupRouteTableArgs{
			RouteTableId: &id,
		}, lookupOpts...)
		if err != nil {
			return nil, err
		}
		for _, assoc := range table.Associations {
			if assoc.SubnetId != "" && subnetSet[assoc.SubnetId] {
				routeTableIDs = append(routeTableIDs, table.Id)
				break
			}
		}
	}
	return routeTableIDs, nil
}

// createVPCEndpointSG creates the VPC endpoint security group. vpcID is the VPC
// the SG belongs to (works in both greenfield and adoption mode).
func (v *AwsVpc) createVPCEndpointSG(vpcID pulumi.StringInput) error {
	sgName := fmt.Sprintf("%s-vpc-endpoint", v.name)
	sgParentOpts := []pulumi.ResourceOption{
		v.aliasFunc("aws:ec2/securityGroup:SecurityGroup", sgName),
	}
	if v.vpc != nil {
		sgParentOpts = append(sgParentOpts, pulumi.Parent(v.vpc))
	}
	sgOpts := v.appendProvider(sgParentOpts)
	sg, err := awsec2.NewSecurityGroup(v.ctx, sgName, &awsec2.SecurityGroupArgs{
		Description: pulumi.Sprintf("%s VPC endpoint", v.name),
		NamePrefix:  pulumi.Sprintf("%s-vpc-endpoint-", v.name),
		Ingress: awsec2.SecurityGroupIngressArray{
			awsec2.SecurityGroupIngressArgs{
				FromPort:   pulumi.Int(443),
				ToPort:     pulumi.Int(443),
				Protocol:   pulumi.String("tcp"),
				CidrBlocks: pulumi.StringArray{pulumi.String(v.cidrBlock)},
			},
		},
		VpcId: vpcID,
		Tags:  v.tagsWithName(sgName),
	}, sgOpts...)
	if err != nil {
		return fmt.Errorf("VPC endpoint SG: %w", err)
	}
	v.vpcEndpointSG = sg
	return nil
}

// naclRule creates a NetworkAclRule with the alias pointing to the Python URN.
func (v *AwsVpc) naclRule(
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
func (v *AwsVpc) createPublicNACLBaseRules() error {
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

// createPrivateNACLBaseRules creates the 5 base rules for the private NACL.
func (v *AwsVpc) createPrivateNACLBaseRules() error {
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

// WithNACLRule adds a NACL rule to either the public or private NACL.
// This replicates AWSVpc.with_nacl_rule() from Python.
//
// fromPort/toPort give an inclusive port range. For an all-protocol rule pass
// protocol -1 and fromPort/toPort 0 (Python collapses a single int into
// from==to and a range into from..to). Persistent passes ranges such as
// 111, 2049, 20001-20002, and 0-65535 (all ports).
//
// Rule numbers are drawn from per-(privacy, direction) counters that start at
// 2000 and increment by 500 on each call (matching the Python behavior). The
// resource name uses the counter value as the rule id, matching the Python
// single-CIDR-per-call naming (persistent always passes a single CIDR block).
func (v *AwsVpc) WithNACLRule(
	privacy string, // "public" or "private"
	fromPort, toPort int, // inclusive port range
	protocol int, // 6=tcp, 17=udp, -1=all
	cidr string,
	egress bool,
) error {
	var nacl *awsec2.NetworkAcl
	var ruleNum *int
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
	resourceName := fmt.Sprintf("%s-%s-%s-rule-%d", v.name, privacy, direction, *ruleNum)
	protoStr := fmt.Sprintf("%d", protocol)

	if err := v.naclRule(resourceName, nacl, *ruleNum, egress, protoStr, fromPort, toPort, cidr, "allow"); err != nil {
		return err
	}
	*ruleNum += 500
	return nil
}

// WithSecureDefaultSecurityGroup locks down the default security group (EC2.2).
func (v *AwsVpc) WithSecureDefaultSecurityGroup() error {
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

// WithSecureDefaultNACL locks down the default NACL (EC2.21).
func (v *AwsVpc) WithSecureDefaultNACL() error {
	resourceName := fmt.Sprintf("%s-default", v.name)
	opts := v.appendProvider([]pulumi.ResourceOption{
		v.aliasFunc("aws:ec2/defaultNetworkAcl:DefaultNetworkAcl", resourceName),
	})
	_, err := awsec2.NewDefaultNetworkAcl(v.ctx, resourceName, &awsec2.DefaultNetworkAclArgs{
		DefaultNetworkAclId: v.vpc.DefaultNetworkAclId,
	}, opts...)
	return err
}

// WithEndpoint creates a VPC endpoint for the given service name.
// Gateway type (s3, dynamodb) gets route table associations.
// Interface type gets private DNS and a security group: the provided
// securityGroupIDs, or the builder's vpc-endpoint SG when none are given
// (mirrors Python with_endpoint(service, security_group_ids=None)).
func (v *AwsVpc) WithEndpoint(service string, securityGroupIDs ...pulumi.StringInput) error {
	endpointType := "Gateway"
	if service != "s3" && service != "dynamodb" {
		endpointType = "Interface"
	}

	var lookupOpts []pulumi.InvokeOption
	if invokeOpt := v.invokeProvider(); invokeOpt != nil {
		lookupOpts = append(lookupOpts, invokeOpt)
	}
	svc, err := awsec2.LookupVpcEndpointService(v.ctx, &awsec2.LookupVpcEndpointServiceArgs{
		Service:     strPtr(service),
		ServiceType: strPtr(endpointType),
	}, lookupOpts...)
	if err != nil {
		return fmt.Errorf("lookup VPC endpoint service %q: %w", service, err)
	}

	endpointName := fmt.Sprintf("%s-%s", v.name, service)
	epOpts := []pulumi.ResourceOption{
		v.aliasFunc("aws:ec2/vpcEndpoint:VpcEndpoint", endpointName),
	}
	if v.vpc != nil {
		epOpts = append(epOpts, pulumi.Parent(v.vpc))
	}
	opts := v.appendProvider(epOpts)

	args := &awsec2.VpcEndpointArgs{
		ServiceName:     pulumi.String(svc.ServiceName),
		VpcEndpointType: pulumi.String(svc.ServiceType),
		VpcId:           v.VpcID(),
		Tags:            v.tagsWithName(endpointName),
	}

	if strings.EqualFold(svc.ServiceType, "Gateway") {
		routeTableIDs := pulumi.StringArray{}
		for _, rt := range v.privateRouteTables {
			routeTableIDs = append(routeTableIDs, rt.ID())
		}
		for _, id := range v.existingPrivateRouteTblID {
			routeTableIDs = append(routeTableIDs, pulumi.String(id))
		}
		if v.publicRouteTable != nil {
			routeTableIDs = append(routeTableIDs, v.publicRouteTable.ID())
		}
		args.RouteTableIds = routeTableIDs
	} else {
		args.PrivateDnsEnabled = pulumi.Bool(true)
		if len(securityGroupIDs) > 0 {
			sgIDs := make(pulumi.StringArray, len(securityGroupIDs))
			for i, id := range securityGroupIDs {
				sgIDs[i] = id
			}
			args.SecurityGroupIds = sgIDs
		} else {
			args.SecurityGroupIds = pulumi.StringArray{v.vpcEndpointSG.ID()}
		}
		// Interface endpoints attach to the public subnets in greenfield mode.
		// In adoption mode there are no public subnets created here; the Python
		// code only ever creates interface endpoints (fsx) on adopted VPCs and
		// relies on private DNS, so leave SubnetIds unset.
		if len(v.publicSubnets) > 0 {
			subnetIDs := make(pulumi.StringArray, len(v.publicSubnets))
			for i, s := range v.publicSubnets {
				subnetIDs[i] = s.ID()
			}
			args.SubnetIds = subnetIDs
		}
	}

	_, err = awsec2.NewVpcEndpoint(v.ctx, endpointName, args, opts...)
	return err
}

// WithNATGateways creates one EIP + NAT gateway per public subnet plus a
// default route in each private route table.
func (v *AwsVpc) WithNATGateways() error {
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
		v.natGwPublicIps = append(v.natGwPublicIps, ngw.PublicIp)

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

// vpcFlowLogFields is the ordered set of fields emitted in the VPC flow-log
// custom format (matches Python aws_vpc.py with_flow_log).
var vpcFlowLogFields = []string{
	"version", "account-id", "vpc-id", "subnet-id", "interface-id",
	"flow-direction", "action", "srcaddr", "srcport", "dstaddr", "dstport",
	"protocol", "packets", "bytes", "start", "end", "log-status",
}

// WithFlowLog enables VPC Flow Logs (mirrors Python with_flow_log).
//
// It creates a CloudWatch LogGroup ("<name>-flow-logs-group"), creates a
// FlowLogs IAM role when roleARN is nil (honoring permissionsBoundary), and one
// aws.ec2.FlowLog per destination (the new LogGroup plus each existing target
// ARN).
func (v *AwsVpc) WithFlowLog(
	permissionsBoundary, roleARN *string,
	existingFlowLogTargetARNs []string,
) error {
	flowLogsGroupName := fmt.Sprintf("%s-flow-logs-group", v.name)
	flGroupOpts := []pulumi.ResourceOption{
		v.aliasFunc("aws:cloudwatch/logGroup:LogGroup", flowLogsGroupName),
	}
	if v.vpc != nil {
		flGroupOpts = append(flGroupOpts, pulumi.Parent(v.vpc))
	}
	flGroupOpts = v.appendProvider(flGroupOpts)
	flowLogsGroup, err := awscloudwatch.NewLogGroup(v.ctx, flowLogsGroupName, &awscloudwatch.LogGroupArgs{
		Name:            pulumi.String(fmt.Sprintf("%s-VPCFlowLogs", v.name)),
		RetentionInDays: pulumi.Int(30),
		Tags:            v.tagsOnly(),
	}, flGroupOpts...)
	if err != nil {
		return fmt.Errorf("flow logs LogGroup: %w", err)
	}

	// Build the ordered list of log destinations: existing targets first, then
	// the newly-created LogGroup (matching Python ordering).
	logDestinations := make([]pulumi.StringInput, 0, len(existingFlowLogTargetARNs)+1)
	for _, arn := range existingFlowLogTargetARNs {
		logDestinations = append(logDestinations, pulumi.String(arn))
	}
	logDestinations = append(logDestinations, flowLogsGroup.Arn)

	// Resolve the IAM role ARN: use the provided one, or create a FlowLogs role.
	var iamRoleARN pulumi.StringInput
	if roleARN != nil && *roleARN != "" {
		iamRoleARN = pulumi.String(*roleARN)
	} else {
		roleName := fmt.Sprintf("%s-flow-logs-iam-role.posit.team", v.name)
		role, rerr := v.createFlowLogsRole(roleName, permissionsBoundary)
		if rerr != nil {
			return rerr
		}
		iamRoleARN = role.Arn
	}

	logFormat := buildFlowLogFormat(vpcFlowLogFields)

	// Python created every FlowLog with the SAME logical name ("<name>-flow-log").
	// Pulumi requires unique logical names, but only ONE destination existed in
	// practice for workloads/control rooms that ran this (no existing targets),
	// so the single resource keeps the Python name verbatim. When additional
	// existing targets are configured, suffix the extras to keep names unique;
	// the primary (LogGroup) destination retains the exact Python name.
	for i, dest := range logDestinations {
		flowLogName := fmt.Sprintf("%s-flow-log", v.name)
		if i < len(logDestinations)-1 {
			// extra destinations (existing target ARNs) get a unique suffix;
			// the last entry is always the LogGroup and keeps the bare name.
			flowLogName = fmt.Sprintf("%s-flow-log-%d", v.name, i)
		}
		flOpts := []pulumi.ResourceOption{
			v.aliasFunc("aws:ec2/flowLog:FlowLog", flowLogName),
		}
		if v.vpc != nil {
			flOpts = append(flOpts, pulumi.Parent(v.vpc))
		}
		flOpts = v.appendProvider(flOpts)
		if _, err := awsec2.NewFlowLog(v.ctx, flowLogName, &awsec2.FlowLogArgs{
			IamRoleArn:             iamRoleARN,
			LogDestination:         dest,
			TrafficType:            pulumi.String("ALL"),
			MaxAggregationInterval: pulumi.Int(60),
			LogFormat:              pulumi.String(logFormat),
			VpcId:                  v.VpcID(),
			Tags:                   v.tagsWithName(v.name),
		}, flOpts...); err != nil {
			return fmt.Errorf("flow log %q: %w", flowLogName, err)
		}
	}

	return nil
}

// createFlowLogsRole creates the VPC FlowLogs IAM role and its inline policy,
// mirroring Python AWSVpc.create_flow_logs_role.
func (v *AwsVpc) createFlowLogsRole(name string, permissionsBoundary *string) (*awsiam.Role, error) {
	var lookupOpts []pulumi.InvokeOption
	if invokeOpt := v.invokeProvider(); invokeOpt != nil {
		lookupOpts = append(lookupOpts, invokeOpt)
	}

	assumeRolePolicy, err := awsiam.GetPolicyDocument(v.ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{
				Actions: []string{"sts:AssumeRole"},
				Principals: []awsiam.GetPolicyDocumentStatementPrincipal{
					{Type: "Service", Identifiers: []string{"vpc-flow-logs.amazonaws.com"}},
				},
			},
		},
	}, lookupOpts...)
	if err != nil {
		return nil, fmt.Errorf("flow logs assume-role policy: %w", err)
	}

	policy, err := awsiam.GetPolicyDocument(v.ctx, &awsiam.GetPolicyDocumentArgs{
		Statements: []awsiam.GetPolicyDocumentStatement{
			{
				Effect: strPtr("Allow"),
				Actions: []string{
					"logs:CreateLogGroup",
					"logs:CreateLogStream",
					"logs:PutLogEvents",
					"logs:DescribeLogGroups",
					"logs:DescribeLogStreams",
				},
				Resources: []string{"*"},
			},
		},
	}, lookupOpts...)
	if err != nil {
		return nil, fmt.Errorf("flow logs role policy doc: %w", err)
	}

	roleArgs := &awsiam.RoleArgs{
		Name:             pulumi.String(name),
		AssumeRolePolicy: pulumi.String(assumeRolePolicy.Json),
	}
	if permissionsBoundary != nil && *permissionsBoundary != "" {
		roleArgs.PermissionsBoundary = pulumi.String(*permissionsBoundary)
	}

	roleOpts := []pulumi.ResourceOption{
		v.aliasFunc("aws:iam/role:Role", name),
		// Python merged in an alias to a role named "flow-logs-role".
		pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String("flow-logs-role")}}),
	}
	if v.vpc != nil {
		roleOpts = append(roleOpts, pulumi.Parent(v.vpc))
	}
	roleOpts = v.appendProvider(roleOpts)
	role, err := awsiam.NewRole(v.ctx, name, roleArgs, roleOpts...)
	if err != nil {
		return nil, fmt.Errorf("flow logs role: %w", err)
	}

	rolePolicyName := fmt.Sprintf("%s-role-policy", name)
	rpOpts := []pulumi.ResourceOption{
		v.aliasFunc("aws:iam/rolePolicy:RolePolicy", rolePolicyName),
	}
	rpOpts = v.appendProvider(rpOpts)
	if _, err := awsiam.NewRolePolicy(v.ctx, rolePolicyName, &awsiam.RolePolicyArgs{
		Name:   pulumi.String(rolePolicyName),
		Role:   role.ID(),
		Policy: pulumi.String(policy.Json),
	}, rpOpts...); err != nil {
		return nil, fmt.Errorf("flow logs role policy: %w", err)
	}

	return role, nil
}

// buildFlowLogFormat renders the flow-log custom format, e.g.
// "${version} ${account-id} ...", matching Python's join.
func buildFlowLogFormat(fields []string) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fmt.Sprintf("${%s}", f)
	}
	return strings.Join(parts, " ")
}

// tagsWithName returns the base tags merged with a "Name" key.
func (v *AwsVpc) tagsWithName(name string) pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, val := range v.tags {
		out[k] = val
	}
	out["Name"] = pulumi.String(name)
	return out
}

// tagsOnly returns a copy of the base tags (no "Name" key added).
func (v *AwsVpc) tagsOnly() pulumi.StringMap {
	out := pulumi.StringMap{}
	for k, val := range v.tags {
		out[k] = val
	}
	return out
}

// strPtr is a helper that returns a pointer to a string (for optional args).
func strPtr(s string) *string { return &s }
