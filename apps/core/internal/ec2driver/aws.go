package ec2driver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudcontroller"
	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"gopkg.in/yaml.v3"
)

const (
	managedKey     = "sous:managed"
	managedValue   = "worker-v1"
	scopeKey       = "sous:scope"
	reservationKey = "sous:reservation"
	generationKey  = "sous:generation"
)

type awsAPI interface {
	DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeInstanceTypeOfferings(context.Context, *ec2.DescribeInstanceTypeOfferingsInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	RunInstances(context.Context, *ec2.RunInstancesInput, ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}

type ec2Access struct {
	client awsAPI
	cfg    Config
}

// OpenAWS checks the caller account first. The controller does not read or
// copy credentials and never forwards an AWS role to a worker.
func OpenAWS(ctx context.Context, c Config) (ec2Access, error) {
	if err := c.validate(); err != nil {
		return ec2Access{}, err
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(c.Region), awsconfig.WithRetryMaxAttempts(1))
	if err != nil {
		return ec2Access{}, errors.New("cannot load the AWS deployment role")
	}
	stsURL := "sts." + c.Region + ".amazonaws.com"
	ec2URL := "ec2." + c.Region + ".amazonaws.com"
	token := sts.NewFromConfig(cfg, func(o *sts.Options) {
		o.BaseEndpoint = aws.String("https://" + stsURL)
		o.EndpointResolver = nil
		o.HTTPClient = regionalHTTP(stsURL)
	})
	who, err := token.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil || who == nil || aws.ToString(who.Account) != c.Account {
		return ec2Access{}, errors.New("AWS role is unavailable or does not match the configured account")
	}
	return ec2Access{client: ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String("https://" + ec2URL)
		o.EndpointResolver = nil
		o.HTTPClient = regionalHTTP(ec2URL)
	}), cfg: c}, nil
}

type regionTransport struct {
	host string
	t    *http.Transport
}

func (r regionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != r.host || req.URL.User != nil {
		return nil, errors.New("unexpected AWS service endpoint")
	}
	return r.t.RoundTrip(req)
}
func regionalHTTP(host string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{Transport: regionTransport{host, tr}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func apiCode(err error) string {
	var e smithy.APIError
	if errors.As(err, &e) {
		return e.ErrorCode()
	}
	return ""
}

// An explicit rejection on the FIRST attempt can prove no resource was
// created. A later error never erases any older unresolved transmission.
func definitelyRejected(err error) bool {
	switch apiCode(err) {
	case "UnauthorizedOperation", "AuthFailure", "InvalidAMIID.NotFound", "InvalidSubnetID.NotFound", "InvalidGroup.NotFound", "InvalidParameterValue", "Unsupported", "InsufficientInstanceCapacity", "InstanceLimitExceeded", "VcpuLimitExceeded":
		return true
	}
	return false
}

// Verify is read-only and is deliberately separate from the controller. It
// tests operator authentication, AMI and network, not instance launch or KVM.
func Verify(ctx context.Context, c Config) error {
	a, err := OpenAWS(ctx, c)
	if err != nil {
		return err
	}
	_, err = a.checkLaunch(ctx)
	return err
}

func (a ec2Access) checkLaunch(ctx context.Context) (string, error) {
	c := a.cfg
	images, err := a.client.DescribeImages(ctx, &ec2.DescribeImagesInput{ImageIds: []string{c.AMI}, Owners: []string{c.Account}})
	if err != nil || images == nil || len(images.Images) != 1 {
		return "", errors.New("EC2 could not verify the pinned worker AMI")
	}
	i := images.Images[0]
	root := aws.ToString(i.RootDeviceName)
	if aws.ToString(i.ImageId) != c.AMI || aws.ToString(i.OwnerId) != c.Account || i.State != types.ImageStateAvailable || i.Architecture != types.ArchitectureValuesX8664 || i.RootDeviceType != types.DeviceTypeEbs || root == "" || len(i.BlockDeviceMappings) != 1 {
		return "", errors.New("worker AMI must be account-owned x86_64 with exactly one EBS root")
	}
	b := i.BlockDeviceMappings[0]
	if aws.ToString(b.DeviceName) != root || b.Ebs == nil || aws.ToString(b.Ebs.SnapshotId) == "" || aws.ToInt32(b.Ebs.VolumeSize) > c.DiskGiB {
		return "", errors.New("worker AMI root does not fit the pinned encrypted disk plan")
	}
	sub, err := a.client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{c.Subnet}})
	if err != nil || sub == nil || len(sub.Subnets) != 1 || aws.ToString(sub.Subnets[0].SubnetId) != c.Subnet || aws.ToString(sub.Subnets[0].OwnerId) != c.Account || sub.Subnets[0].State != types.SubnetStateAvailable || aws.ToBool(sub.Subnets[0].AssignIpv6AddressOnCreation) || aws.ToBool(sub.Subnets[0].Ipv6Native) {
		return "", errors.New("worker subnet must be account-owned, available, and not auto-assign public IPv6")
	}
	groups, err := a.client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{c.Group}})
	if err != nil || groups == nil || len(groups.SecurityGroups) != 1 || aws.ToString(groups.SecurityGroups[0].GroupId) != c.Group || aws.ToString(groups.SecurityGroups[0].OwnerId) != c.Account || aws.ToString(groups.SecurityGroups[0].VpcId) != aws.ToString(sub.Subnets[0].VpcId) || len(groups.SecurityGroups[0].IpPermissions) != 0 {
		return "", errors.New("worker security group must be account-owned, in the selected VPC, and have no ingress")
	}
	zone := aws.ToString(sub.Subnets[0].AvailabilityZone)
	if zone == "" || aws.ToString(sub.Subnets[0].VpcId) == "" {
		return "", errors.New("EC2 could not identify the subnet's VPC and availability zone")
	}
	offers, err := a.client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: types.LocationTypeAvailabilityZone,
		Filters:      []types.Filter{{Name: aws.String("location"), Values: []string{zone}}, {Name: aws.String("instance-type"), Values: []string{c.InstanceType}}},
	})
	if err != nil || offers == nil || len(offers.InstanceTypeOfferings) != 1 || aws.ToString(offers.InstanceTypeOfferings[0].Location) != zone || offers.InstanceTypeOfferings[0].InstanceType != types.InstanceType(c.InstanceType) {
		return "", errors.New("the pinned nested-virtualization instance type is not confirmed in the subnet's availability zone")
	}
	return root, nil
}

func clientToken(r cloudcontroller.Request) string {
	return cloudwork.Hash([]byte("sous-ec2-v1\x00" + r.Scope + "\x00" + r.Reservation))
}
func tag(k, v string) types.Tag { return types.Tag{Key: aws.String(k), Value: aws.String(v)} }
func tags(r cloudcontroller.Request) []types.Tag {
	return []types.Tag{tag(managedKey, managedValue), tag(scopeKey, r.Scope), tag(reservationKey, r.Reservation), tag(generationKey, strconv.FormatInt(r.Generation, 10))}
}

func userdata(r cloudcontroller.Request) (string, error) {
	type bootstrapFile struct {
		Path        string `yaml:"path"`
		Owner       string `yaml:"owner"`
		Permissions string `yaml:"permissions"`
		Content     string `yaml:"content"`
	}
	body, err := yaml.Marshal(struct {
		Files    []bootstrapFile `yaml:"write_files"`
		Commands [][]string      `yaml:"runcmd"`
	}{
		[]bootstrapFile{
			{"/etc/sous-ec2/worker.env", "root:root", "0600", fmt.Sprintf("SOUS_HUB=%s\nSOUS_GENERATION=%d\n", r.Hub, r.Generation)},
			{"/run/sous-ec2-bootstrap/worker-token", "sous-worker:sous-worker", "0400", r.Bootstrap + "\n"},
		}, [][]string{{"systemctl", "enable", "--now", "sous-ec2-worker.service"}},
	})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append([]byte("#cloud-config\n"), body...)), nil
}

func (a ec2Access) launchInput(r cloudcontroller.Request, root string) (*ec2.RunInstancesInput, error) {
	c := a.cfg
	ud, err := userdata(r)
	if err != nil {
		return nil, err
	}
	return &ec2.RunInstancesInput{
		ImageId: aws.String(c.AMI), InstanceType: types.InstanceType(c.InstanceType), MinCount: aws.Int32(1), MaxCount: aws.Int32(1), ClientToken: aws.String(clientToken(r)),
		CpuOptions:                        &types.CpuOptionsRequest{NestedVirtualization: types.NestedVirtualizationSpecificationEnabled},
		NetworkInterfaces:                 []types.InstanceNetworkInterfaceSpecification{{DeviceIndex: aws.Int32(0), SubnetId: aws.String(c.Subnet), Groups: []string{c.Group}, AssociatePublicIpAddress: aws.Bool(false), DeleteOnTermination: aws.Bool(true)}},
		MetadataOptions:                   &types.InstanceMetadataOptionsRequest{HttpEndpoint: types.InstanceMetadataEndpointStateEnabled, HttpTokens: types.HttpTokensStateRequired, HttpPutResponseHopLimit: aws.Int32(1), InstanceMetadataTags: types.InstanceMetadataTagsStateDisabled},
		BlockDeviceMappings:               []types.BlockDeviceMapping{{DeviceName: aws.String(root), Ebs: &types.EbsBlockDevice{Encrypted: aws.Bool(true), DeleteOnTermination: aws.Bool(true), VolumeSize: aws.Int32(c.DiskGiB), VolumeType: types.VolumeTypeGp3}}},
		TagSpecifications:                 []types.TagSpecification{{ResourceType: types.ResourceTypeInstance, Tags: tags(r)}, {ResourceType: types.ResourceTypeVolume, Tags: tags(r)}},
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorStop, DisableApiTermination: aws.Bool(false), UserData: aws.String(ud),
	}, nil
}

func scopeFilters(r cloudcontroller.Request) []types.Filter {
	return []types.Filter{{Name: aws.String("tag:" + managedKey), Values: []string{managedValue}}, {Name: aws.String("tag:" + scopeKey), Values: []string{r.Scope}}}
}

func matches(r cloudcontroller.Request, t []types.Tag) bool {
	values := map[string]string{}
	for _, tag := range t {
		values[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return values[managedKey] == managedValue && values[scopeKey] == r.Scope && values[reservationKey] == r.Reservation && values[generationKey] == strconv.FormatInt(r.Generation, 10)
}

type instance struct {
	id         string
	rootVolume string
	state      types.InstanceStateName
}

func (a ec2Access) observe(ctx context.Context, r cloudcontroller.Request) (instance, error) {
	var found instance
	input := ec2.DescribeInstancesInput{Filters: scopeFilters(r), MaxResults: aws.Int32(100)}
	for page := 0; page < 10; page++ {
		out, err := a.client.DescribeInstances(ctx, &input)
		if err != nil || out == nil {
			return instance{}, errors.New("EC2 instance reconciliation not confirmed")
		}
		for _, res := range out.Reservations {
			if aws.ToString(res.OwnerId) != a.cfg.Account {
				return instance{}, errors.New("unexpected owner in EC2 reservation scan")
			}
			for _, i := range res.Instances {
				if i.State == nil || !knownState(i.State.Name) {
					return instance{}, errors.New("unknown EC2 instance state")
				}
				same := matches(r, i.Tags) && aws.ToString(i.ClientToken) == clientToken(r)
				if !same && i.State.Name == types.InstanceStateNameTerminated {
					continue
				}
				if !same || !instancePattern.MatchString(aws.ToString(i.InstanceId)) || found.id != "" {
					return instance{}, errors.New("ambiguous or orphaned EC2 instance in this workspace; no change made")
				}
				volume, err := workerVolume(i)
				if err != nil {
					return instance{}, err
				}
				found = instance{id: aws.ToString(i.InstanceId), state: i.State.Name, rootVolume: volume}
			}
		}
		if aws.ToString(out.NextToken) == "" {
			return found, nil
		}
		input.NextToken = out.NextToken
	}
	return instance{}, errors.New("EC2 instance reconciliation exceeded page limit")
}

func workerVolume(i types.Instance) (string, error) {
	if len(i.BlockDeviceMappings) == 0 {
		return "", nil
	}
	if len(i.BlockDeviceMappings) != 1 || i.BlockDeviceMappings[0].Ebs == nil {
		return "", errors.New("unexpected volumes on EC2 worker; no automatic termination")
	}
	b := i.BlockDeviceMappings[0].Ebs
	if (b.DeleteOnTermination != nil && !aws.ToBool(b.DeleteOnTermination)) || !volumePattern.MatchString(aws.ToString(b.VolumeId)) {
		return "", errors.New("EC2 worker root is not marked for automatic deletion or has an unknown identity")
	}
	return aws.ToString(b.VolumeId), nil
}

func knownState(state types.InstanceStateName) bool {
	switch state {
	case types.InstanceStateNamePending, types.InstanceStateNameRunning, types.InstanceStateNameShuttingDown,
		types.InstanceStateNameTerminated, types.InstanceStateNameStopping, types.InstanceStateNameStopped:
		return true
	default:
		return false
	}
}

func (a ec2Access) noVolumes(ctx context.Context, r cloudcontroller.Request, root string) (bool, error) {
	filters := append(scopeFilters(r), types.Filter{Name: aws.String("tag:" + reservationKey), Values: []string{r.Reservation}})
	out, err := a.client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: filters, MaxResults: aws.Int32(50)})
	if err != nil || out == nil {
		return false, errors.New("EC2 EBS cleanup not confirmed")
	}
	if len(out.Volumes) != 0 || aws.ToString(out.NextToken) != "" {
		return false, nil
	}
	if root == "" {
		return true, nil
	}
	// Check the exact remembered root too, so removed tags do not look like
	// EBS deletion. Never delete a leftover disk automatically.
	out, err = a.client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{root}})
	if apiCode(err) == "InvalidVolume.NotFound" {
		return true, nil
	}
	if err != nil || out == nil {
		return false, errors.New("EC2 root volume deletion not confirmed")
	}
	return len(out.Volumes) == 0 && aws.ToString(out.NextToken) == "", nil
}
