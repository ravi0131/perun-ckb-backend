package channel

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types/molecule"
	"math"
	"perun.network/go-perun/channel"
	"perun.network/perun-ckb-backend/client"
	"perun.network/perun-ckb-backend/encoding"
	"time"
)

const (
	DefaultBufferSize                  = 3
	DefaultSubscriptionPollingInterval = time.Duration(4) * time.Second
)

type PollingSubscription struct {
	PollingInterval time.Duration
	client          client.CKBClient
	id              channel.ID
	pcts            *types.Script
	events          chan channel.AdjudicatorEvent
	err             error
	cancel          context.CancelFunc
}

func NewAdjudicatorSubFromChannelID(ctx context.Context, ckbClient client.CKBClient, id channel.ID) *PollingSubscription {
	sub := &PollingSubscription{
		PollingInterval: DefaultSubscriptionPollingInterval,
		client:          ckbClient,
		id:              id,
		events:          make(chan channel.AdjudicatorEvent, DefaultBufferSize),
	}
	ctx, sub.cancel = context.WithCancel(ctx)
	go sub.run(ctx)
	return sub
}

func (a *PollingSubscription) run(ctx context.Context) {
	finish := func() {
		a.err = fmt.Errorf("subscription closed by context: %w", ctx.Err())
		close(a.events)
	}
	var oldStatus *molecule.ChannelStatus
	for {
		select {
		case <-ctx.Done():
			finish()
			return
		case <-time.After(a.PollingInterval):
			blockNumber, newStatus, err := a.pollStatus(ctx)
			if err != nil {
				// TODO: What happens if the channel is closed on-chain?
				continue
			}
			statusDidChange := a.emitEventIfNecessary(ctx, oldStatus, newStatus, blockNumber)
			if statusDidChange {
				oldStatus = newStatus
			}
		}

	}
}

func (a *PollingSubscription) pollStatus(ctx context.Context) (client.BlockNumber, *molecule.ChannelStatus, error) {
	if a.pcts != nil {
		return a.client.GetChannelWithExactPCTS(ctx, a.pcts)
	}
	b, pcts, _, status, err := a.client.GetChannelWithID(ctx, a.id)
	if err != nil {
		return 0, nil, err
	}
	a.pcts = pcts
	return b, status, nil
}

// emitEventIfNecessary emits an event if the difference between oldStatus and newStatus indicates an event.
// It returns ture, iff the status has changed.
// Note: The status can change without warranting emission of an event!
func (a *PollingSubscription) emitEventIfNecessary(ctx context.Context, oldStatus *molecule.ChannelStatus, newStatus *molecule.ChannelStatus, newBlockNumber client.BlockNumber) bool {
	if newStatus == nil {
		// TODO: How can this happen?
		return false
	}
	if oldStatus != nil && bytes.Equal(oldStatus.AsSlice(), newStatus.AsSlice()) {
		return false
	}
	if !encoding.ToBool(*oldStatus.Funded()) {
		return true
	}
	if !encoding.ToBool(*newStatus.Disputed()) {
		panic(fmt.Sprintf(
			"channel received update but is not disputed. oldStatus: %s, newStatus: %s",
			hex.EncodeToString(oldStatus.AsSlice()),
			hex.EncodeToString(newStatus.AsSlice()),
		))
	}
	// TODO: Handle conclude event.
	challengeDurationStart, err := a.getChallengeDurationStart(ctx, newBlockNumber)
	if err != nil {
		panic(fmt.Sprintf("could not get challenge duration start: %v", err))
	}

	challengeDuration, err := a.getChallengeDuration()
	if err != nil {
		panic(fmt.Sprintf("could not get challenge duration: %v", err))
	}

	event := channel.NewRegisteredEvent(
		a.id,
		&channel.TimeTimeout{Time: challengeDurationStart.Add(challengeDuration)},
		encoding.UnpackUint64(newStatus.State().Version()),
		nil, // only needed for virtual channels
		nil, // only needed for virtual channels
	)
	a.events <- event
	return true
}

func (a *PollingSubscription) EventStream() <-chan channel.AdjudicatorEvent {
	return a.events
}

func (a *PollingSubscription) Err() error {
	return a.err
}
func (a *PollingSubscription) Close() error {
	a.cancel()
	return nil
}

// TODO: Maybe cache this information.
func (a *PollingSubscription) getChallengeDuration() (time.Duration, error) {
	if a.pcts == nil {
		return 0, fmt.Errorf("cannot get challenge duration: pcts not set")
	}
	channelConstants, err := molecule.ChannelConstantsFromSlice(a.pcts.Args, false)
	if err != nil {
		return 0, err
	}

	duration := encoding.UnpackUint64(channelConstants.Params().ChallengeDuration())
	if duration > math.MaxInt64 {
		panic(fmt.Sprintf("challenge duration %d is too large, max: %d", duration, math.MaxInt64))
	}
	return time.Duration(duration) * time.Millisecond, nil
}

func (a *PollingSubscription) getChallengeDurationStart(ctx context.Context, blockNumber client.BlockNumber) (time.Time, error) {
	const retries = 5
	var challengeDurationStart time.Time
	var err error
	for i := 0; i < retries; i++ {
		challengeDurationStart, err = a.client.GetBlockTime(ctx, blockNumber)
		if err == nil {
			break
		}
	}
	return challengeDurationStart, err
}
