// Copyright 2015 The Cockroach Authors.
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
	"reflect"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/storage/engine/enginepb"
	"github.com/cockroachdb/cockroach/pkg/storage/stateloader"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/stop"
	"github.com/kr/pretty"
)

// initialStats are the stats for a Replica which has been created through
// bootstrapRangeOnly. These stats are not empty because we call
// writeInitialState().
func initialStats() enginepb.MVCCStats {
	return enginepb.MVCCStats{
		SysBytes: 98,
		SysCount: 3,
	}
}
func TestRangeStatsEmpty(t *testing.T) {
	defer leaktest.AfterTest(t)()
	tc := testContext{
		bootstrapMode: bootstrapRangeOnly,
	}
	stopper := stop.NewStopper()
	defer stopper.Stop(context.TODO())
	tc.Start(t, stopper)

	ms := tc.repl.GetMVCCStats()
	if exp := initialStats(); !reflect.DeepEqual(ms, exp) {
		t.Errorf("unexpected stats diff(exp, actual):\n%s", pretty.Diff(exp, ms))
	}
}

func TestRangeStatsInit(t *testing.T) {
	defer leaktest.AfterTest(t)()
	tc := testContext{}
	stopper := stop.NewStopper()
	defer stopper.Stop(context.TODO())
	tc.Start(t, stopper)
	ms := enginepb.MVCCStats{
		LiveBytes:       1,
		KeyBytes:        2,
		ValBytes:        3,
		IntentBytes:     4,
		LiveCount:       5,
		KeyCount:        6,
		ValCount:        7,
		IntentCount:     8,
		IntentAge:       9,
		GCBytesAge:      10,
		LastUpdateNanos: 11,
	}
	rsl := stateloader.Make(tc.repl.RangeID)
	if err := rsl.SetMVCCStats(context.Background(), tc.engine, &ms); err != nil {
		t.Fatal(err)
	}
	loadMS, err := rsl.LoadMVCCStats(context.Background(), tc.engine)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ms, loadMS) {
		t.Errorf("mvcc stats mismatch %+v != %+v", ms, loadMS)
	}
}
