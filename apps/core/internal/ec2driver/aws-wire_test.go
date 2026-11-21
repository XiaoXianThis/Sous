//go:build linux || darwin

package ec2driver

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// This sends a real, SigV4-signed EC2 Query request through the AWS SDK to an
// isolated local HTTP fixture. It never reads personal credentials or calls AWS.
func TestAWSWireUsesFixedPrivateNestedLaunch(t *testing.T) {
	c := sampleConfig(t)
	r := reserve(c, 1, time.Now())
	received := make(chan url.Values, 1)
	v := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, h *http.Request) {
		if h.ParseForm() != nil || !strings.HasPrefix(h.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(400)
			return
		}
		received <- h.Form
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<RunInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>fixture</requestId><ownerId>%s</ownerId><instancesSet><item><instanceId>%s</instanceId><instanceState><name>pending</name></instanceState></item></instancesSet></RunInstancesResponse>`, c.Account, instanceFixture)
	}))
	defer v.Close()
	client := ec2.New(ec2.Options{Region: c.Region, BaseEndpoint: aws.String(v.URL), HTTPClient: v.Client(), Retryer: aws.NopRetryer{}, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "fixture-key", SecretAccessKey: "fixture-secret"}, nil
	})})
	a := ec2Access{client: client, cfg: c}
	in, err := a.launchInput(r, "/dev/sda1")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := client.RunInstances(context.Background(), in); err != nil || len(out.Instances) != 1 || aws.ToString(out.OwnerId) != c.Account {
		t.Fatal("AWS Query fixture did not recognize SDK request", err)
	}
	form := <-received
	for k, want := range map[string]string{
		"Action": "RunInstances", "MinCount": "1", "MaxCount": "1", "ClientToken": clientToken(r),
		"CpuOptions.NestedVirtualization": "enabled", "MetadataOptions.HttpTokens": "required", "MetadataOptions.HttpPutResponseHopLimit": "1",
		"NetworkInterface.1.AssociatePublicIpAddress": "false", "NetworkInterface.1.SubnetId": c.Subnet,
		"BlockDeviceMapping.1.Ebs.Encrypted": "true", "BlockDeviceMapping.1.Ebs.DeleteOnTermination": "true", "BlockDeviceMapping.1.Ebs.VolumeType": "gp3",
	} {
		if form.Get(k) != want {
			t.Errorf("EC2 query field %s=#{%s}, wanted %s", k, form.Get(k), want)
		}
	}
	if form.Get("IamInstanceProfile.Arn") != "" || form.Get("IamInstanceProfile.Name") != "" || form.Get("KeyName") != "" {
		t.Fatal("instance received operator identity or SSH credentials")
	}
	raw, err := base64.StdEncoding.DecodeString(form.Get("UserData"))
	if err != nil || !strings.HasPrefix(string(raw), "#cloud-config\n") || !strings.Contains(string(raw), "sous-ec2-worker.service") || !strings.Contains(string(raw), r.Bootstrap) || !strings.Contains(string(raw), "0400") {
		t.Fatal("bad short-lived worker bootstrap")
	}
	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("operator credentials were forwarded")
	}
}

func TestAWSWireSettlesOnlyTheObservedInstanceAndRootVolume(t *testing.T) {
	c := sampleConfig(t)
	ctx := context.Background()
	r := retireRequest(reserve(c, 1, time.Now()))
	if err := Init(c); err != nil {
		t.Fatal(err)
	}
	f, err := openLedger(c)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var expected []string
	records := make(chan url.Values, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, h *http.Request) {
		if h.ParseForm() != nil || !strings.HasPrefix(h.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		form := h.Form
		records <- form
		w.Header().Set("Content-Type", "text/xml")
		switch form.Get("Action") {
		case "DescribeInstances":
			if form.Get("Filter.1.Name") != "tag:"+managedKey || form.Get("Filter.2.Value.1") != r.Scope {
				t.Error("AWS scan lost workspace scope")
			}
			fmt.Fprintf(w, `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><ownerId>%s</ownerId><instancesSet><item><instanceId>%s</instanceId><clientToken>%s</clientToken><instanceState><name>running</name></instanceState><tagSet>`, c.Account, instanceFixture, clientToken(r))
			for _, tag := range tags(r) {
				fmt.Fprintf(w, "<item><key>%s</key><value>%s</value></item>", aws.ToString(tag.Key), aws.ToString(tag.Value))
			}
			fmt.Fprintf(w, `</tagSet><blockDeviceMapping><item><deviceName>/dev/sda1</deviceName><ebs><volumeId>%s</volumeId><deleteOnTermination>true</deleteOnTermination></ebs></item></blockDeviceMapping></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, volumeFixture)
		case "TerminateInstances":
			if form.Get("InstanceId.1") != instanceFixture || form.Get("InstanceId.2") != "" {
				t.Error("AWS termination address was not the positively identified instance")
			}
			fmt.Fprintf(w, `<TerminateInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><instancesSet><item><instanceId>%s</instanceId><currentState><name>terminated</name></currentState></item></instancesSet></TerminateInstancesResponse>`, instanceFixture)
		case "DescribeVolumes":
			if form.Get("VolumeId.1") == volumeFixture {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `<Response><Errors><Error><Code>InvalidVolume.NotFound</Code><Message>fixture removed</Message></Error></Errors><RequestID>fixture</RequestID></Response>`)
			} else {
				if form.Get("Filter.3.Name") != "tag:"+reservationKey || form.Get("Filter.3.Value.1") != r.Reservation {
					t.Error("AWS tagged disk scan lost reservation identity")
				}
				fmt.Fprint(w, `<DescribeVolumesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><volumeSet/></DescribeVolumesResponse>`)
			}
		default:
			t.Error("unexpected AWS operation during retirement")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := ec2.New(ec2.Options{Region: c.Region, BaseEndpoint: aws.String(server.URL), HTTPClient: server.Client(), Retryer: aws.NopRetryer{}, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "fixture-key", SecretAccessKey: "fixture-secret"}, nil
	})})
	current, err := f.load(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reconcile(ctx, ec2Access{client, c}, f, current, r, time.Now)
	if err != nil || got != "absent" {
		t.Fatal("signed AWS flow did not settle known terminated slot", got, err)
	}
	expected = []string{"DescribeInstances", "TerminateInstances", "DescribeVolumes", "DescribeVolumes"}
	close(records)
	for form := range records {
		if len(expected) == 0 || form.Get("Action") != expected[0] {
			t.Fatal("unexpected AWS operation order", form.Get("Action"))
		}
		expected = expected[1:]
	}
	if len(expected) != 0 {
		t.Fatal("release skipped required AWS evidence", expected)
	}
	last, err := f.load(c)
	if err != nil || last.Phase != "off" || !last.Terminal || last.RootVolume != volumeFixture {
		t.Fatal("release evidence was not persisted", last, err)
	}
}
