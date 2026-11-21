//go:build linux || darwin

package ec2driver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudcontroller"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

const instanceFixture = "i-1234567890abcdef0"
const volumeFixture = "vol-1234567890abcdef0"
const vpcFixture = "vpc-1234567890abcdef0"

type cloudFake struct {
	cfg                                   Config
	seen                                  []types.Instance
	volumes, taggedVolumes                []types.Volume
	sent                                  []*ec2.RunInstancesInput
	term                                  []string
	queryRoot                             []string
	onLaunch                              func(*ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error)
	terminating                           types.InstanceStateName
	publicSubnet, ingress, additionalDisk bool
}

func (f *cloudFake) DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	mappings := []types.BlockDeviceMapping{{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsBlockDevice{SnapshotId: aws.String("snap-1234567890abcdef0"), VolumeSize: aws.Int32(12)}}}
	if f.additionalDisk {
		mappings = append(mappings, mappings[0])
	}
	return &ec2.DescribeImagesOutput{Images: []types.Image{{ImageId: aws.String(f.cfg.AMI), OwnerId: aws.String(f.cfg.Account), Architecture: types.ArchitectureValuesX8664, State: types.ImageStateAvailable, RootDeviceName: aws.String("/dev/sda1"), RootDeviceType: types.DeviceTypeEbs, BlockDeviceMappings: mappings}}}, nil
}
func (f *cloudFake) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{Subnets: []types.Subnet{{SubnetId: aws.String(f.cfg.Subnet), OwnerId: aws.String(f.cfg.Account), VpcId: aws.String(vpcFixture), AvailabilityZone: aws.String("eu-central-1a"), State: types.SubnetStateAvailable, AssignIpv6AddressOnCreation: aws.Bool(f.publicSubnet)}}}, nil
}
func (f *cloudFake) DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	out := types.SecurityGroup{GroupId: aws.String(f.cfg.Group), OwnerId: aws.String(f.cfg.Account), VpcId: aws.String(vpcFixture)}
	if f.ingress {
		out.IpPermissions = []types.IpPermission{{IpProtocol: aws.String("tcp")}}
	}
	return &ec2.DescribeSecurityGroupsOutput{SecurityGroups: []types.SecurityGroup{out}}, nil
}
func (f *cloudFake) DescribeInstanceTypeOfferings(context.Context, *ec2.DescribeInstanceTypeOfferingsInput, ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	return &ec2.DescribeInstanceTypeOfferingsOutput{InstanceTypeOfferings: []types.InstanceTypeOffering{{Location: aws.String("eu-central-1a"), InstanceType: types.InstanceType(f.cfg.InstanceType)}}}, nil
}
func (f *cloudFake) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: []types.Reservation{{OwnerId: aws.String(f.cfg.Account), Instances: f.seen}}}, nil
}
func (f *cloudFake) DescribeVolumes(_ context.Context, in *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if len(in.VolumeIds) != 0 {
		f.queryRoot = append(f.queryRoot, in.VolumeIds...)
		return &ec2.DescribeVolumesOutput{Volumes: f.volumes}, nil
	}
	return &ec2.DescribeVolumesOutput{Volumes: f.taggedVolumes}, nil
}
func (f *cloudFake) RunInstances(_ context.Context, in *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	f.sent = append(f.sent, in)
	if f.onLaunch != nil {
		return f.onLaunch(in)
	}
	return &ec2.RunInstancesOutput{OwnerId: aws.String(f.cfg.Account), Instances: []types.Instance{{InstanceId: aws.String(instanceFixture)}}}, nil
}
func (f *cloudFake) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	f.term = append(f.term, in.InstanceIds...)
	return &ec2.TerminateInstancesOutput{TerminatingInstances: []types.InstanceStateChange{{InstanceId: aws.String(instanceFixture), CurrentState: &types.InstanceState{Name: f.terminating}}}}, nil
}

func sampleConfig(t *testing.T) Config {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{Hub: "https://sous.example.test", Account: "123456789012", Region: "eu-central-1", AMI: "ami-1234567890abcdef0", Subnet: "subnet-1234567890abcdef0", Group: "sg-1234567890abcdef0", InstanceType: "c8i.large", DiskGiB: 16, StateDir: dir}
}
func reserve(c Config, n int64, now time.Time) cloudcontroller.Request {
	return cloudcontroller.Request{Action: "ensure", Scope: c.scope(), Generation: n, Reservation: ids.New(), Hub: c.Hub, Bootstrap: strings.Repeat("f", 64), BootstrapUntil: now.Add(10 * time.Minute).UnixMilli()}
}
func retireRequest(r cloudcontroller.Request) cloudcontroller.Request {
	r.Action, r.Bootstrap, r.BootstrapUntil = "retire", "", 0
	return r
}
func owned(r cloudcontroller.Request, state types.InstanceStateName) types.Instance {
	return types.Instance{InstanceId: aws.String(instanceFixture), State: &types.InstanceState{Name: state}, ClientToken: aws.String(clientToken(r)), Tags: tags(r), BlockDeviceMappings: []types.InstanceBlockDeviceMapping{{DeviceName: aws.String("/dev/sda1"), Ebs: &types.EbsInstanceBlockDevice{VolumeId: aws.String(volumeFixture), DeleteOnTermination: aws.Bool(true)}}}}
}
func one(ctx context.Context, c Config, f *cloudFake, r cloudcontroller.Request, now time.Time) (string, error) {
	db, err := openLedger(c)
	if err != nil {
		return "", err
	}
	defer db.Close()
	s, err := db.load(c)
	if err != nil {
		return "", err
	}
	return reconcile(ctx, ec2Access{f, c}, db, s, r, func() time.Time { return now })
}

func TestUnknownLaunchRetiresOnlyAfterPositiveInstanceAndDiskEvidence(t *testing.T) {
	c := sampleConfig(t)
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	r := reserve(c, 1, now)
	f := &cloudFake{cfg: c, terminating: types.InstanceStateNameShuttingDown}
	f.onLaunch = func(in *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
		// Check the intent ON DISK at the API boundary. The process then loses the response.
		d, err := os.ReadFile(filepath.Join(c.StateDir, ledgerName))
		if err != nil {
			t.Fatal(err)
		}
		var s Ledger
		if json.Unmarshal(d, &s) != nil || s.Attempts != 1 || s.Reservation != r.Reservation || strings.Contains(string(d), r.Bootstrap) {
			t.Fatal("unsafe launch ledger")
		}
		return nil, context.DeadlineExceeded
	}
	if got, err := one(ctx, c, f, r, now); got != "pending" || err == nil {
		t.Fatal(got, err)
	}
	stop := retireRequest(r)
	if got, err := one(ctx, c, f, stop, now); got != "pending" || err != nil || len(f.term) != 0 {
		t.Fatalf("negative describe released unknown operation: %s %v", got, err)
	}
	if got, err := one(ctx, c, f, r, now.Add(time.Minute)); got != "pending" || err == nil || len(f.sent) != 1 {
		t.Fatal("late ensure crossed retirement fence", got, err)
	}
	f.seen = []types.Instance{owned(r, types.InstanceStateNameRunning)}
	if got, err := one(ctx, c, f, stop, now); got != "pending" || err != nil || !reflect.DeepEqual(f.term, []string{instanceFixture}) {
		t.Fatal(got, err, f.term)
	}
	f.seen = nil
	if got, err := one(ctx, c, f, stop, now); got != "pending" || err != nil {
		t.Fatal("a disappearing instance is not terminal proof", got, err)
	}
	f.seen = []types.Instance{owned(r, types.InstanceStateNameTerminated)}
	f.volumes = []types.Volume{{VolumeId: aws.String(volumeFixture)}} // tags may have been removed
	if got, err := one(ctx, c, f, stop, now); got != "pending" || err != nil || len(f.queryRoot) != 1 {
		t.Fatal("retained disk ignored", got, err)
	}
	f.seen = nil // positive termination was persisted even if the record is purged
	f.volumes = nil
	if got, err := one(ctx, c, f, stop, now); got != "absent" || err != nil {
		t.Fatal(got, err)
	}
	if got, err := one(ctx, c, f, stop, now); got != "absent" || err != nil || len(f.term) != 1 {
		t.Fatal("retire not idempotent", got, err)
	}
	f.onLaunch = nil
	if got, err := one(ctx, c, f, reserve(c, 2, now), now); got != "present" || err != nil || len(f.sent) != 2 {
		t.Fatal("cannot wake new generation", got, err)
	}
}

func TestSameZonalLaunchIsRetriedButNoLaunchOnRetirement(t *testing.T) {
	c := sampleConfig(t)
	ctx := context.Background()
	now := time.Now()
	r := reserve(c, 1, now)
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	f := &cloudFake{cfg: c}
	f.onLaunch = func(*ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) { return nil, context.DeadlineExceeded }
	_, _ = one(ctx, c, f, r, now)
	if _, err := one(ctx, c, f, r, now.Add(time.Second)); err != nil || len(f.sent) != 1 {
		t.Fatal("backoff was ignored", err)
	}
	r2 := r
	r2.Bootstrap = strings.Repeat("c", 64)
	if _, err := one(ctx, c, f, r2, now.Add(time.Minute)); err == nil || len(f.sent) != 1 {
		t.Fatal("changed launch parameters were submitted", err)
	}
	f.onLaunch = nil
	if got, err := one(ctx, c, f, r, now.Add(time.Minute)); got != "present" || err != nil || len(f.sent) != 2 || !reflect.DeepEqual(f.sent[0], f.sent[1]) {
		t.Fatal("retry did not preserve the identical zonal launch request", got, err)
	}
	if got, err := one(ctx, c, f, retireRequest(r), now); got != "pending" || err != nil || len(f.sent) != 2 {
		t.Fatal("retire launched or assumed absent", got, err)
	}
}

func TestColdCancellationFencesEvenIfEnsureArrivesLate(t *testing.T) {
	c := sampleConfig(t)
	ctx := context.Background()
	now := time.Now()
	r := reserve(c, 1, now)
	f := &cloudFake{cfg: c}
	if _, err := one(ctx, c, f, r, now); err == nil {
		t.Fatal("missing ledger treated as initial capacity")
	}
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	if err := Init(c); err == nil {
		t.Fatal("ledger reinitialized")
	}
	if got, err := one(ctx, c, f, retireRequest(r), now); got != "absent" || err != nil {
		t.Fatal(got, err)
	}
	if _, err := one(ctx, c, f, r, now); err == nil || len(f.sent) != 0 {
		t.Fatal("late ensure launched a cancelled reservation", err)
	}
	changed := c
	changed.Region = "eu-west-1"
	if _, err := Status(changed); err == nil {
		t.Fatal("ledger rebound to another deployment")
	}
}

func TestScopeAndPreflightPreventUnauthorizedProviderChanges(t *testing.T) {
	c := sampleConfig(t)
	ctx := context.Background()
	now := time.Now()
	r := reserve(c, 1, now)
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	f := &cloudFake{cfg: c, ingress: true}
	if _, err := one(ctx, c, f, r, now); err == nil || len(f.sent) != 0 {
		t.Fatal("public ingress accepted", err)
	}
	f.ingress = false
	f.additionalDisk = true
	if _, err := one(ctx, c, f, r, now); err == nil || len(f.sent) != 0 {
		t.Fatal("AMI with unexpected disk accepted", err)
	}
	f.additionalDisk = false
	f.publicSubnet = true
	if _, err := one(ctx, c, f, r, now); err == nil || len(f.sent) != 0 {
		t.Fatal("automatic public IPv6 accepted", err)
	}
	f.publicSubnet = false
	f.seen = []types.Instance{owned(reserve(c, 2, now), types.InstanceStateNameRunning)}
	if _, err := one(ctx, c, f, retireRequest(r), now); err == nil || len(f.term) != 0 {
		t.Fatal("orphan incorrectly changed", err)
	}
	db, err := openLedger(c)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if second, err := openLedger(c); err == nil {
		second.Close()
		t.Fatal("parallel provider mutation allowed")
	}
}

func TestDefinitiveRejectionDoesNotEraseOlderUnknownAttempt(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "first", true: "later"}[uncertain], func(t *testing.T) {
			c := sampleConfig(t)
			ctx := context.Background()
			now := time.Now()
			r := reserve(c, 1, now)
			if err := Init(c); err != nil {
				t.Fatal(err)
			}
			f := &cloudFake{cfg: c, onLaunch: func(*ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) { return nil, errors.New("lost") }}
			if uncertain {
				_, _ = one(ctx, c, f, r, now)
			}
			f.onLaunch = func(*ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
				return nil, &smithy.GenericAPIError{Code: "InsufficientInstanceCapacity"}
			}
			_, _ = one(ctx, c, f, r, now.Add(time.Minute))
			want := "absent"
			if uncertain {
				want = "pending"
			}
			if got, err := one(ctx, c, f, retireRequest(r), now.Add(time.Minute)); got != want || err != nil {
				t.Fatal(got, err)
			}
		})
	}
}

func TestBootWindowCannotExpireDuringAWSPreflight(t *testing.T) {
	c := sampleConfig(t)
	now := time.Now()
	r := reserve(c, 1, now)
	f := &cloudFake{cfg: c}
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	db, err := openLedger(c)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := db.load(c)
	if err != nil {
		t.Fatal(err)
	}
	first := true
	clock := func() time.Time {
		if first {
			first = false
			return now
		}
		return now.Add(9*time.Minute + 45*time.Second)
	}
	if _, err := reconcile(context.Background(), ec2Access{f, c}, db, s, r, clock); err == nil || len(f.sent) != 0 {
		t.Fatal("expired start was dispatched", err)
	}
	if current, err := db.load(c); err != nil || current.Attempts != 0 {
		t.Fatal("failed preflight recorded a launch", current, err)
	}
}
