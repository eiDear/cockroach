// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License included
// in the file licenses/BSL.txt and at www.mariadb.com/bsl11.
//
// Change Date: 2022-10-01
//
// On the date above, in accordance with the Business Source License, use
// of this software will be governed by the Apache License, Version 2.0,
// included in the file licenses/APL.txt and at
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"errors"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/config"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/storage/storagepb"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/metric"
	"github.com/cockroachdb/cockroach/pkg/util/stop"
	"github.com/cockroachdb/cockroach/pkg/util/tracing"
	"golang.org/x/sync/errgroup"
)

// TestBaseQueueConcurrent verifies that under concurrent adds/removes of ranges
// to the queue including purgatory errors and regular errors, the queue
// invariants are upheld. The test operates on fake ranges and a mock queue
// impl, which are defined at the end of the file.
func TestBaseQueueConcurrent(t *testing.T) {
	defer leaktest.AfterTest(t)()

	ctx := context.Background()
	stopper := stop.NewStopper()
	defer stopper.Stop(ctx)

	// We'll use this many ranges, each of which is added a few times to the
	// queue and maybe removed as well.
	const num = 1000

	cfg := queueConfig{
		maxSize:              num / 2,
		maxConcurrency:       4,
		acceptsUnsplitRanges: true,
		processTimeout:       time.Millisecond,
		// We don't care about these, but we don't want to crash.
		successes:       metric.NewCounter(metric.Metadata{Name: "processed"}),
		failures:        metric.NewCounter(metric.Metadata{Name: "failures"}),
		pending:         metric.NewGauge(metric.Metadata{Name: "pending"}),
		processingNanos: metric.NewCounter(metric.Metadata{Name: "processingnanos"}),
		purgatory:       metric.NewGauge(metric.Metadata{Name: "purgatory"}),
	}

	// Set up a fake store with just exactly what the code calls into. Ideally
	// we'd set up an interface against the *Store as well, similar to
	// replicaInQueue, but this isn't an ideal world. Deal with it.
	store := &Store{
		cfg: StoreConfig{
			Clock:             hlc.NewClock(hlc.UnixNano, time.Second),
			AmbientCtx:        log.AmbientContext{Tracer: tracing.NewTracer()},
			DefaultZoneConfig: config.DefaultZoneConfigRef(),
		},
	}

	// Set up a queue impl that will return random results from processing.
	impl := fakeQueueImpl{
		pr: func(context.Context, *Replica, *config.SystemConfig) error {
			n := rand.Intn(4)
			if n == 0 {
				return nil
			} else if n == 1 {
				return errors.New("injected regular error")
			} else if n == 2 {
				return &benignError{errors.New("injected benign error")}
			}
			return &testPurgatoryError{}
		},
	}
	bq := newBaseQueue("test", impl, store, nil /* Gossip */, cfg)
	bq.getReplica = func(id roachpb.RangeID) (replicaInQueue, error) {
		return &fakeReplica{id: id}, nil
	}
	bq.Start(stopper)

	var g errgroup.Group
	for i := 1; i <= num; i++ {
		r := &fakeReplica{id: roachpb.RangeID(i)}
		for j := 0; j < 5; j++ {
			g.Go(func() error {
				_, err := bq.testingAdd(ctx, r, 1.0)
				return err
			})
		}
		if rand.Intn(5) == 0 {
			g.Go(func() error {
				bq.MaybeRemove(r.id)
				return nil
			})
		}
		g.Go(func() error {
			bq.assertInvariants()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	for done := false; !done; {
		bq.mu.Lock()
		done = len(bq.mu.replicas) == 0
		bq.mu.Unlock()
		runtime.Gosched()
	}
}

type fakeQueueImpl struct {
	pr func(context.Context, *Replica, *config.SystemConfig) error
}

func (fakeQueueImpl) shouldQueue(
	context.Context, hlc.Timestamp, *Replica, *config.SystemConfig,
) (shouldQueue bool, priority float64) {
	return rand.Intn(5) != 0, 1.0
}

func (fq fakeQueueImpl) process(
	ctx context.Context, repl *Replica, cfg *config.SystemConfig,
) error {
	return fq.pr(ctx, repl, cfg)
}

func (fakeQueueImpl) timer(time.Duration) time.Duration {
	return time.Nanosecond
}

func (fakeQueueImpl) purgatoryChan() <-chan time.Time {
	return time.After(time.Nanosecond)
}

type fakeReplica struct {
	id roachpb.RangeID
}

func (fr *fakeReplica) AnnotateCtx(ctx context.Context) context.Context { return ctx }
func (fr *fakeReplica) StoreID() roachpb.StoreID {
	return 1
}
func (fr *fakeReplica) GetRangeID() roachpb.RangeID         { return fr.id }
func (fr *fakeReplica) IsInitialized() bool                 { return true }
func (fr *fakeReplica) IsDestroyed() (DestroyReason, error) { return destroyReasonAlive, nil }
func (fr *fakeReplica) Desc() *roachpb.RangeDescriptor {
	return &roachpb.RangeDescriptor{RangeID: fr.id, EndKey: roachpb.RKey("z")}
}
func (fr *fakeReplica) maybeInitializeRaftGroup(context.Context) {}
func (fr *fakeReplica) redirectOnOrAcquireLease(
	context.Context,
) (storagepb.LeaseStatus, *roachpb.Error) {
	// baseQueue only checks that the returned error is nil.
	return storagepb.LeaseStatus{}, nil
}
func (fr *fakeReplica) IsLeaseValid(roachpb.Lease, hlc.Timestamp) bool { return true }
func (fr *fakeReplica) GetLease() (roachpb.Lease, roachpb.Lease) {
	return roachpb.Lease{}, roachpb.Lease{}
}
