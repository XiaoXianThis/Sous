package ec2driver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudcontroller"
	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

const maxLaunchAttempts = 3
const minLaunchGap = 10 * time.Second

// Run invokes the production AWS adapter, with an account check before any
// mutating API. All shutdown evidence is retained in the service-owned ledger.
func Run(ctx context.Context, c Config, r cloudcontroller.Request) (string, error) {
	if err := c.validateRequest(r); err != nil {
		return "", err
	}
	f, err := openLedger(c)
	if err != nil {
		return "", err
	}
	defer f.Close()
	local, err := f.load(c)
	if err != nil {
		return "", err
	}
	a, err := OpenAWS(ctx, c)
	if err != nil {
		return "", err
	}
	return reconcile(ctx, a, f, local, r, time.Now)
}

func reconcile(ctx context.Context, a ec2Access, f *ledgerFile, s Ledger, r cloudcontroller.Request, clock func() time.Time) (string, error) {
	now := clock()
	if err := a.cfg.validateRequest(r); err != nil {
		return "", err
	}
	const pending = "pending"
	if r.Generation < s.Generation {
		if r.Action == "retire" {
			return "absent", nil
		}
		return pending, errors.New("EC2 launch generation is fenced by the ledger")
	}
	if r.Generation == s.Generation && r.Reservation != s.Reservation {
		return pending, errors.New("EC2 generation already bound to another reservation")
	}
	if r.Generation == s.Generation && s.Phase == "off" {
		if r.Action == "retire" {
			return "absent", nil
		}
		return pending, errors.New("EC2 reservation was already retired")
	}
	if r.Generation != s.Generation && (s.Phase != "off" || r.Generation-s.Generation != 1) {
		return pending, errors.New("previous EC2 generation has not been settled, or ledger history is incomplete")
	}
	if r.Action == "ensure" && (s.Phase == "retiring" || r.BootstrapUntil <= now.UnixMilli() || r.BootstrapUntil > now.Add(11*time.Minute).UnixMilli()) {
		return pending, errors.New("EC2 boot is no longer authorized")
	}
	if r.Generation != s.Generation {
		s = Ledger{Version: s.Version, Binding: s.Binding, Generation: r.Generation, Reservation: r.Reservation, Phase: "open"}
	}
	if r.Action == "retire" && s.Phase != "retiring" {
		s.Phase = "retiring"
		// Persist the tombstone before any more I/O. A delayed ensure must not
		// restart an instance while describe/terminate are settling.
		if err := f.save(s); err != nil {
			return pending, err
		}
	}
	found, err := a.observe(ctx, r)
	if err != nil {
		return pending, err
	}
	if found.id != "" {
		if s.InstanceID != "" && found.id != s.InstanceID {
			return pending, errors.New("EC2 reported a different instance for the same reservation")
		}
		if s.Terminal && found.state != types.InstanceStateNameTerminated {
			return pending, errors.New("EC2 returned a nonterminal instance after termination was recorded")
		}
		s.InstanceID = found.id
		if found.rootVolume != "" {
			if s.RootVolume != "" && s.RootVolume != found.rootVolume {
				return pending, errors.New("EC2 root volume changed after launch; retain for reconciliation")
			}
			s.RootVolume = found.rootVolume
		}
		s.Terminal = found.state == types.InstanceStateNameTerminated
		if err := f.save(s); err != nil {
			return pending, err
		}
	}
	if r.Action == "retire" {
		return retireEC2(ctx, a, f, s, r, found)
	}
	if s.Terminal {
		return pending, errors.New("EC2 instance ended before worker registration")
	}
	if s.InstanceID != "" {
		return "present", nil
	}
	if s.Rejected || s.Attempts >= maxLaunchAttempts || (s.Attempts > 0 && now.UnixMilli()-s.LastLaunchAt < minLaunchGap.Milliseconds()) {
		return pending, nil
	}
	root, err := a.checkLaunch(ctx)
	if err != nil {
		return pending, err
	}
	in, err := a.launchInput(r, root)
	if err != nil {
		return pending, err
	}
	planBytes, err := json.Marshal(in)
	if err != nil {
		return pending, err
	}
	plan := cloudwork.Hash(planBytes)
	if s.Plan != "" && s.Plan != plan {
		return pending, errors.New("refusing to retry an EC2 request with changed parameters")
	}
	// Operator verification can take several API requests; recheck the
	// grant immediately before the durable launch intent. Leave a minimum
	// window for boot, rather than starting an already-expiring worker.
	launchTime := clock()
	if ctx.Err() != nil || r.BootstrapUntil <= launchTime.Add(30*time.Second).UnixMilli() {
		return pending, errors.New("remaining EC2 boot authorization is too short to launch")
	}
	// The proof of a potential send is durable BEFORE invoking RunInstances.
	// If the process is killed in the SDK, retire reconciles but never launches.
	s.Plan = plan
	s.Attempts++
	s.LastLaunchAt = launchTime.UnixMilli()
	if err := f.save(s); err != nil {
		return pending, err
	}
	out, err := a.client.RunInstances(ctx, in)
	if err != nil {
		if s.Attempts == 1 && definitelyRejected(err) {
			s.Rejected = true
			if fsErr := f.save(s); fsErr != nil {
				return pending, fsErr
			}
		}
		return pending, errors.New("EC2 launch result must be reconciled")
	}
	if out == nil || len(out.Instances) != 1 || aws.ToString(out.OwnerId) != a.cfg.Account || !instancePattern.MatchString(aws.ToString(out.Instances[0].InstanceId)) {
		return pending, errors.New("EC2 launch did not confirm a single owned instance")
	}
	s.InstanceID = aws.ToString(out.Instances[0].InstanceId)
	volume, err := workerVolume(out.Instances[0])
	if err != nil {
		return pending, err
	}
	s.RootVolume = volume
	if err := f.save(s); err != nil {
		return pending, err
	}
	return "present", nil
}

func retireEC2(ctx context.Context, a ec2Access, f *ledgerFile, s Ledger, r cloudcontroller.Request, found instance) (string, error) {
	const pending = "pending"
	if s.InstanceID != "" && !s.Terminal {
		// A negative Describe is not termination proof, even for a known ID.
		if found.id == "" || found.state == types.InstanceStateNameShuttingDown {
			return pending, nil
		}
		out, err := a.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{s.InstanceID}})
		if err != nil || out == nil || len(out.TerminatingInstances) != 1 || aws.ToString(out.TerminatingInstances[0].InstanceId) != s.InstanceID || out.TerminatingInstances[0].CurrentState == nil {
			return pending, errors.New("EC2 termination not confirmed")
		}
		if out.TerminatingInstances[0].CurrentState.Name != types.InstanceStateNameTerminated {
			return pending, nil
		}
		s.Terminal = true
		if err := f.save(s); err != nil {
			return pending, err
		}
	}
	if !s.Terminal && s.Attempts > 0 && !s.Rejected {
		// Never treat a missing eventually consistent result as absence. This also
		// covers a process crash between persisting intent and transmitting it.
		// Resolve in the provider before releasing capacity; do not launch on retire.
		return pending, nil
	}
	clear, err := a.noVolumes(ctx, r, s.RootVolume)
	if err != nil || !clear {
		return pending, err
	}
	s.Phase = "off"
	if err := f.save(s); err != nil {
		return pending, err
	}
	return "absent", nil
}
