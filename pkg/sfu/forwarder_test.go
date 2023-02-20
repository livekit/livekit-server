package sfu

import (
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"

	"github.com/livekit/protocol/logger"

	"github.com/livekit/livekit-server/pkg/sfu/buffer"
	"github.com/livekit/livekit-server/pkg/sfu/testutils"
)

func disable(f *Forwarder) {
	f.currentLayers = InvalidLayers
	f.targetLayers = InvalidLayers
}

func newForwarder(codec webrtc.RTPCodecCapability, kind webrtc.RTPCodecType) *Forwarder {
	f := NewForwarder(kind, logger.GetLogger(), nil)
	f.DetermineCodec(codec)
	return f
}

func TestForwarderMute(t *testing.T) {
	f := newForwarder(testutils.TestOpusCodec, webrtc.RTPCodecTypeAudio)
	require.False(t, f.IsMuted())
	muted, _ := f.Mute(false)
	require.False(t, muted) // no change in mute state
	require.False(t, f.IsMuted())
	muted, _ = f.Mute(true)
	require.True(t, muted)
	require.True(t, f.IsMuted())
	muted, _ = f.Mute(false)
	require.True(t, muted)
	require.False(t, f.IsMuted())
}

func TestForwarderLayersAudio(t *testing.T) {
	f := newForwarder(testutils.TestOpusCodec, webrtc.RTPCodecTypeAudio)

	require.Equal(t, InvalidLayers, f.MaxLayers())

	require.Equal(t, InvalidLayers, f.CurrentLayers())
	require.Equal(t, InvalidLayers, f.TargetLayers())

	changed, maxLayers, currentLayers := f.SetMaxSpatialLayer(1)
	require.False(t, changed)
	require.Equal(t, InvalidLayers, maxLayers)
	require.Equal(t, InvalidLayers, currentLayers)

	changed, maxLayers, currentLayers = f.SetMaxTemporalLayer(1)
	require.False(t, changed)
	require.Equal(t, InvalidLayers, maxLayers)
	require.Equal(t, InvalidLayers, currentLayers)

	require.Equal(t, InvalidLayers, f.MaxLayers())
}

func TestForwarderLayersVideo(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	maxLayers := f.MaxLayers()
	expectedLayers := VideoLayers{Spatial: InvalidLayerSpatial, Temporal: DefaultMaxLayerTemporal}
	require.Equal(t, expectedLayers, maxLayers)

	require.Equal(t, InvalidLayers, f.CurrentLayers())
	require.Equal(t, InvalidLayers, f.TargetLayers())

	expectedLayers = VideoLayers{
		Spatial:  DefaultMaxLayerSpatial,
		Temporal: DefaultMaxLayerTemporal,
	}
	changed, maxLayers, currentLayers := f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	require.True(t, changed)
	require.Equal(t, expectedLayers, maxLayers)
	require.Equal(t, InvalidLayers, currentLayers)

	changed, maxLayers, currentLayers = f.SetMaxSpatialLayer(DefaultMaxLayerSpatial - 1)
	require.True(t, changed)
	expectedLayers = VideoLayers{
		Spatial:  DefaultMaxLayerSpatial - 1,
		Temporal: DefaultMaxLayerTemporal,
	}
	require.Equal(t, expectedLayers, maxLayers)
	require.Equal(t, expectedLayers, f.MaxLayers())
	require.Equal(t, InvalidLayers, currentLayers)

	f.currentLayers = VideoLayers{Spatial: 0, Temporal: 1}
	changed, maxLayers, currentLayers = f.SetMaxSpatialLayer(DefaultMaxLayerSpatial - 1)
	require.False(t, changed)
	require.Equal(t, expectedLayers, maxLayers)
	require.Equal(t, expectedLayers, f.MaxLayers())
	require.Equal(t, VideoLayers{Spatial: 0, Temporal: 1}, currentLayers)

	changed, maxLayers, currentLayers = f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	require.False(t, changed)
	require.Equal(t, expectedLayers, maxLayers)
	require.Equal(t, VideoLayers{Spatial: 0, Temporal: 1}, currentLayers)

	changed, maxLayers, currentLayers = f.SetMaxTemporalLayer(DefaultMaxLayerTemporal - 1)
	require.True(t, changed)
	expectedLayers = VideoLayers{
		Spatial:  DefaultMaxLayerSpatial - 1,
		Temporal: DefaultMaxLayerTemporal - 1,
	}
	require.Equal(t, expectedLayers, maxLayers)
	require.Equal(t, expectedLayers, f.MaxLayers())
	require.Equal(t, VideoLayers{Spatial: 0, Temporal: 1}, currentLayers)
}

func TestForwarderAllocateOptimal(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	emptyBitrates := Bitrates{}
	bitrates := Bitrates{
		{2, 3, 0, 0},
		{4, 0, 0, 5},
		{0, 7, 0, 0},
	}

	// invalid max layers
	f.maxLayers = InvalidLayers
	expectedResult := VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           InvalidLayers,
		distanceToDesired:   0,
	}
	result := f.AllocateOptimal(nil, bitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)

	// should still have target at InvalidLayers until max publisher layer is available
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, bitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	// muted should not consume any bandwidth
	f.Mute(true)
	disable(f)
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonMuted,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, bitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	f.Mute(false)

	// pub muted should not consume any bandwidth
	f.PubMute(true)
	disable(f)
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonPubMuted,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, bitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	f.PubMute(false)

	// when parked layers valid, should stay there
	f.parkedLayers = VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        f.parkedLayers,
		requestLayerSpatial: f.parkedLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, f.parkedLayers, f.TargetLayers())
	f.parkedLayers = InvalidLayers

	// when max layers changes, should switch to that
	f.maxLayers = VideoLayers{Spatial: 1, Temporal: 3}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        DefaultMaxLayers,
		requestLayerSpatial: f.maxLayers.Spatial,
		maxLayers:           f.maxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, DefaultMaxLayers, f.TargetLayers())

	// reset max layers for rest of the tests below
	f.maxLayers = DefaultMaxLayers
	f.lastAllocation.maxLayers = DefaultMaxLayers

	// when feed is dry and current is not valid, should set up for opportunistic forwarding
	disable(f)
	expectedTargetLayers := VideoLayers{
		Spatial:  2,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	f.targetLayers = VideoLayers{Spatial: 0, Temporal: 0}  // set to valid to trigger paths in tests below
	f.currentLayers = VideoLayers{Spatial: 0, Temporal: 3} // set to valid to trigger paths in tests below

	// when feed is dry and current is valid, should stay at current
	expectedTargetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 3,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	// max layers changing, feed dry, current invalid, no overshoot, should set target to max layers
	f.SetMaxSpatialLayer(0)
	f.SetMaxTemporalLayer(3)
	f.currentLayers = InvalidLayers
	expectedTargetLayers = VideoLayers{
		Spatial:  0,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedMaxLayers := VideoLayers{
		Spatial:  0,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal(nil, emptyBitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	// opportunistic target if feed is not dry and current is not valid, i. e. not forwarding
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        DefaultMaxLayers,
		requestLayerSpatial: 0,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal([]int32{0, 1}, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, DefaultMaxLayers, f.TargetLayers())

	// if feed is not dry and target is not valid, should be opportunistic (with and without overshoot)
	f.targetLayers = InvalidLayers
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: 0,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal([]int32{0, 1}, emptyBitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	f.targetLayers = InvalidLayers
	expectedTargetLayers = VideoLayers{
		Spatial:  2,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: 0,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal([]int32{0, 1}, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)

	// switches to highest available if feed is not dry and current is valid and current is not available
	f.currentLayers = VideoLayers{Spatial: 0, Temporal: 1}
	expectedTargetLayers = VideoLayers{
		Spatial:  1,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            emptyBitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: 0,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.AllocateOptimal([]int32{1}, emptyBitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
}

func TestForwarderProvisionalAllocate(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.ProvisionalAllocatePrepare(bitrates)

	usedBitrate := f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, true, false)
	require.Equal(t, bitrates[0][0], usedBitrate)

	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 2, Temporal: 3}, true, false)
	require.Equal(t, bitrates[2][3]-bitrates[0][0], usedBitrate)

	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 3}, true, false)
	require.Equal(t, bitrates[0][3]-bitrates[2][3], usedBitrate)

	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 1, Temporal: 2}, true, false)
	require.Equal(t, bitrates[1][2]-bitrates[0][3], usedBitrate)

	// available not enough to reach (2, 2), allocating at (2, 2) should not succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][2]-bitrates[1][2]-1, VideoLayers{Spatial: 2, Temporal: 2}, true, false)
	require.Equal(t, int64(0), usedBitrate)

	// committing should set target to (1, 2)
	expectedTargetLayers := VideoLayers{
		Spatial:  1,
		Temporal: 2,
	}
	expectedResult := VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  bitrates[1][2],
		bandwidthDelta:      bitrates[1][2],
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   5,
	}
	result := f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	// when nothing fits and pausing disallowed, should allocate (0, 0)
	f.targetLayers = InvalidLayers
	f.ProvisionalAllocatePrepare(bitrates)
	usedBitrate = f.ProvisionalAllocate(0, VideoLayers{Spatial: 0, Temporal: 0}, false, false)
	require.Equal(t, int64(1), usedBitrate)

	// committing should set target to (0, 0)
	expectedTargetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 0,
	}
	expectedResult = VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  bitrates[0][0],
		bandwidthDelta:      bitrates[0][0] - bitrates[1][2],
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   11,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	//
	// Test allowOvershoot.
	// Max spatial set to 0 and layer 0 bit rates are not available.
	//
	f.SetMaxSpatialLayer(0)
	bitrates = Bitrates{
		{0, 0, 0, 0},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.ProvisionalAllocatePrepare(bitrates)

	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// overshoot should succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 2, Temporal: 3}, false, true)
	require.Equal(t, bitrates[2][3], usedBitrate)

	// overshoot should succeed - this should win as this is lesser overshoot
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 1, Temporal: 3}, false, true)
	require.Equal(t, bitrates[1][3]-bitrates[2][3], usedBitrate)

	// committing should set target to (1, 3)
	expectedTargetLayers = VideoLayers{
		Spatial:  1,
		Temporal: 3,
	}
	expectedMaxLayers := VideoLayers{
		Spatial:  0,
		Temporal: 3,
	}
	expectedResult = VideoAllocation{
		bandwidthRequested:  bitrates[1][3],
		bandwidthDelta:      bitrates[1][3] - 1, // 1 is the last allocation bandwith requested
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   -4,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	//
	// Even if overshoot is allowed, but if higher layers do not have bit rates, should continue with current layer.
	//
	bitrates = Bitrates{
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 0},
	}

	f.currentLayers = VideoLayers{Spatial: 0, Temporal: 2}
	f.ProvisionalAllocatePrepare(bitrates)

	// all the provisional allocations should not succeed because the feed is dry
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// overshoot should not succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 2, Temporal: 3}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// overshoot should not succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 1, Temporal: 3}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// committing should set target to (0, 2), i. e. leave it at current for opportunistic forwarding
	expectedTargetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 2,
	}
	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  bitrates[0][2],
		bandwidthDelta:      bitrates[0][2] - 8, // 8 is the last allocation bandwith requested
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())

	//
	// Same case as above, but current is above max, so target should go to invalid
	//
	f.currentLayers = VideoLayers{Spatial: 1, Temporal: 2}
	f.ProvisionalAllocatePrepare(bitrates)

	// all the provisional allocations below should not succeed because the feed is dry
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// overshoot should not succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 2, Temporal: 3}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	// overshoot should not succeed
	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 1, Temporal: 3}, false, true)
	require.Equal(t, int64(0), usedBitrate)

	expectedResult = VideoAllocation{
		pauseReason:         VideoPauseReasonFeedDry,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, InvalidLayers, f.TargetLayers())
	require.Equal(t, InvalidLayers, f.CurrentLayers())
}

func TestForwarderProvisionalAllocateMute(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.Mute(true)
	f.ProvisionalAllocatePrepare(bitrates)

	usedBitrate := f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, true, false)
	require.Equal(t, int64(0), usedBitrate)

	usedBitrate = f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 1, Temporal: 2}, true, true)
	require.Equal(t, int64(0), usedBitrate)

	// committing should set target to InvalidLayers as track is muted
	expectedResult := VideoAllocation{
		pauseReason:         VideoPauseReasonMuted,
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result := f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, InvalidLayers, f.TargetLayers())
}

func TestForwarderProvisionalAllocateGetCooperativeTransition(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 0, 0},
	}

	f.ProvisionalAllocatePrepare(bitrates)

	// from scratch (InvalidLayers) should give back layer (0, 0)
	expectedTransition := VideoTransition{
		from:           InvalidLayers,
		to:             VideoLayers{Spatial: 0, Temporal: 0},
		bandwidthDelta: 1,
	}
	transition := f.ProvisionalAllocateGetCooperativeTransition(false)
	require.Equal(t, expectedTransition, transition)

	// committing should set target to (0, 0)
	expectedLayers := VideoLayers{Spatial: 0, Temporal: 0}
	expectedResult := VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  1,
		bandwidthDelta:      1,
		bitrates:            bitrates,
		targetLayers:        expectedLayers,
		requestLayerSpatial: expectedLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   9,
	}
	result := f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedLayers, f.TargetLayers())

	// a higher target that is already streaming, just maintain it
	targetLayers := VideoLayers{Spatial: 2, Temporal: 1}
	f.targetLayers = targetLayers
	f.lastAllocation.bandwidthRequested = 10
	expectedTransition = VideoTransition{
		from:           targetLayers,
		to:             targetLayers,
		bandwidthDelta: 0,
	}
	transition = f.ProvisionalAllocateGetCooperativeTransition(false)
	require.Equal(t, expectedTransition, transition)

	// committing should set target to (2, 1)
	expectedLayers = VideoLayers{Spatial: 2, Temporal: 1}
	expectedResult = VideoAllocation{
		bandwidthRequested:  10,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        expectedLayers,
		requestLayerSpatial: expectedLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedLayers, f.TargetLayers())

	// from a target that has become unavailable, should switch to lower available layer
	targetLayers = VideoLayers{Spatial: 2, Temporal: 2}
	f.targetLayers = targetLayers
	expectedTransition = VideoTransition{
		from:           targetLayers,
		to:             VideoLayers{Spatial: 2, Temporal: 1},
		bandwidthDelta: 0,
	}
	transition = f.ProvisionalAllocateGetCooperativeTransition(false)
	require.Equal(t, expectedTransition, transition)

	f.ProvisionalAllocateCommit()

	// mute
	f.Mute(true)
	f.ProvisionalAllocatePrepare(bitrates)

	// mute should send target to InvalidLayers
	expectedTransition = VideoTransition{
		from:           VideoLayers{Spatial: 2, Temporal: 1},
		to:             InvalidLayers,
		bandwidthDelta: -10,
	}
	transition = f.ProvisionalAllocateGetCooperativeTransition(false)
	require.Equal(t, expectedTransition, transition)

	f.ProvisionalAllocateCommit()

	//
	// Test allowOvershoot
	//
	f.Mute(false)
	f.SetMaxSpatialLayer(0)

	bitrates = Bitrates{
		{0, 0, 0, 0},
		{5, 6, 7, 8},
		{9, 10, 0, 0},
	}

	f.targetLayers = InvalidLayers
	f.ProvisionalAllocatePrepare(bitrates)

	// from scratch (InvalidLayers) should go to a layer past maximum as overshoot is allowed
	expectedTransition = VideoTransition{
		from:           InvalidLayers,
		to:             VideoLayers{Spatial: 1, Temporal: 0},
		bandwidthDelta: 5,
	}
	transition = f.ProvisionalAllocateGetCooperativeTransition(true)
	require.Equal(t, expectedTransition, transition)

	// committing should set target to (1, 0)
	expectedLayers = VideoLayers{Spatial: 1, Temporal: 0}
	expectedMaxLayers := VideoLayers{Spatial: 0, Temporal: DefaultMaxLayerTemporal}
	expectedResult = VideoAllocation{
		bandwidthRequested:  5,
		bandwidthDelta:      5,
		bitrates:            bitrates,
		targetLayers:        expectedLayers,
		requestLayerSpatial: expectedLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   -1,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedLayers, f.TargetLayers())

	//
	// Test continuting at current layers when feed is dry
	//
	bitrates = Bitrates{
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 0},
	}

	f.currentLayers = VideoLayers{Spatial: 0, Temporal: 2}
	f.targetLayers = InvalidLayers
	f.ProvisionalAllocatePrepare(bitrates)

	// from scratch (InvalidLayers) should go to current layer
	// NOTE: targetLayer is set to InvalidLayers for testing, but in practice current layers valid and target layers invalid should not happen
	expectedTransition = VideoTransition{
		from:           InvalidLayers,
		to:             VideoLayers{Spatial: 0, Temporal: 2},
		bandwidthDelta: -5, // 5 was the bandwidth needed for the last allocation
	}
	transition = f.ProvisionalAllocateGetCooperativeTransition(true)
	require.Equal(t, expectedTransition, transition)

	// committing should set target to (0, 2)
	expectedLayers = VideoLayers{Spatial: 0, Temporal: 2}
	expectedResult = VideoAllocation{
		bandwidthRequested:  0,
		bandwidthDelta:      -5,
		bitrates:            bitrates,
		targetLayers:        expectedLayers,
		requestLayerSpatial: expectedLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedLayers, f.TargetLayers())

	// committing should set target to current layers to enable opportunistic forwarding
	expectedResult = VideoAllocation{
		bandwidthRequested:  0,
		bandwidthDelta:      0,
		bitrates:            bitrates,
		targetLayers:        expectedLayers,
		requestLayerSpatial: expectedLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   0,
	}
	result = f.ProvisionalAllocateCommit()
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedLayers, f.TargetLayers())
}

func TestForwarderProvisionalAllocateGetBestWeightedTransition(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.ProvisionalAllocatePrepare(bitrates)

	f.targetLayers = VideoLayers{Spatial: 2, Temporal: 2}
	f.lastAllocation.bandwidthRequested = bitrates[2][2]
	expectedTransition := VideoTransition{
		from:           f.targetLayers,
		to:             VideoLayers{Spatial: 2, Temporal: 0},
		bandwidthDelta: 2,
	}
	transition := f.ProvisionalAllocateGetBestWeightedTransition()
	require.Equal(t, expectedTransition, transition)
}

func TestForwarderAllocateNextHigher(t *testing.T) {
	f := newForwarder(testutils.TestOpusCodec, webrtc.RTPCodecTypeAudio)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	emptyBitrates := Bitrates{}
	bitrates := Bitrates{
		{2, 3, 0, 0},
		{4, 0, 0, 5},
		{0, 7, 0, 0},
	}

	result, boosted := f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, VideoAllocationDefault, result) // no layer for audio
	require.False(t, boosted)

	f = newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	// when not in deficient state, does not boost
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, VideoAllocationDefault, result)
	require.False(t, boosted)

	// if layers have not caught up, should not allocate next layer even if deficient
	f.targetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 0,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, VideoAllocationDefault, result)
	require.False(t, boosted)

	f.lastAllocation.isDeficient = true
	f.currentLayers = VideoLayers{
		Spatial:  0,
		Temporal: 0,
	}

	// move from (0, 0) -> (0, 1), i.e. a higher temporal layer is available in the same spatial layer
	expectedTargetLayers := VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	expectedResult := VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  3,
		bandwidthDelta:      1,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   3,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)

	// empty bitrates cannot increase layer, i. e. last allocation is left unchanged
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, emptyBitrates, false)
	require.Equal(t, expectedResult, result)
	require.False(t, boosted)

	// move from (0, 1) -> (1, 0), i.e. a higher spatial layer is available
	f.currentLayers.Temporal = 1
	expectedTargetLayers = VideoLayers{
		Spatial:  1,
		Temporal: 0,
	}
	expectedResult = VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  4,
		bandwidthDelta:      1,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   2,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)

	// next higher, move from (1, 0) -> (1, 3), still deficient though
	f.currentLayers.Spatial = 1
	f.currentLayers.Temporal = 0
	expectedTargetLayers = VideoLayers{
		Spatial:  1,
		Temporal: 3,
	}
	expectedResult = VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  5,
		bandwidthDelta:      1,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   1,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)

	// next higher, move from (1, 3) -> (2, 1), optimal allocation
	f.currentLayers.Temporal = 3
	expectedTargetLayers = VideoLayers{
		Spatial:  2,
		Temporal: 1,
	}
	expectedResult = VideoAllocation{
		bandwidthRequested:  7,
		bandwidthDelta:      2,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)

	// ask again, should return not boosted as there is no room to go higher
	f.currentLayers.Spatial = 2
	f.currentLayers.Temporal = 1
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.False(t, boosted)

	// turn off everything, allocating next layer should result in streaming lowest layers
	disable(f)
	f.lastAllocation.isDeficient = true
	f.lastAllocation.bandwidthRequested = 0

	expectedTargetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 0,
	}
	expectedResult = VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  2,
		bandwidthDelta:      2,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   4,
	}
	result, boosted = f.AllocateNextHigher(ChannelCapacityInfinity, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)

	// no new available capacity cannot bump up layer
	expectedResult = VideoAllocation{
		isDeficient:         true,
		bandwidthRequested:  2,
		bandwidthDelta:      2,
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   4,
	}
	result, boosted = f.AllocateNextHigher(0, bitrates, false)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.False(t, boosted)

	// test allowOvershoot
	f.SetMaxSpatialLayer(0)

	bitrates = Bitrates{
		{0, 0, 0, 0},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.currentLayers = f.targetLayers

	expectedTargetLayers = VideoLayers{
		Spatial:  1,
		Temporal: 0,
	}
	expectedMaxLayers := VideoLayers{
		Spatial:  0,
		Temporal: DefaultMaxLayerTemporal,
	}
	expectedResult = VideoAllocation{
		bandwidthRequested:  bitrates[1][0],
		bandwidthDelta:      bitrates[1][0],
		bitrates:            bitrates,
		targetLayers:        expectedTargetLayers,
		requestLayerSpatial: expectedTargetLayers.Spatial,
		maxLayers:           expectedMaxLayers,
		distanceToDesired:   -1,
	}
	// overshoot should return (1, 0) even if there is not enough capacity
	result, boosted = f.AllocateNextHigher(bitrates[1][0]-1, bitrates, true)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, expectedTargetLayers, f.TargetLayers())
	require.True(t, boosted)
}

func TestForwarderPause(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.ProvisionalAllocatePrepare(bitrates)
	f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, true, false)
	// should have set target at (0, 0)
	f.ProvisionalAllocateCommit()

	expectedResult := VideoAllocation{
		pauseReason:         VideoPauseReasonBandwidth,
		isDeficient:         true,
		bandwidthRequested:  0,
		bandwidthDelta:      0 - bitrates[0][0],
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   12,
	}
	result := f.Pause(bitrates)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, InvalidLayers, f.TargetLayers())
}

func TestForwarderPauseMute(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.SetMaxSpatialLayer(DefaultMaxLayerSpatial)
	f.SetMaxTemporalLayer(DefaultMaxLayerTemporal)
	f.SetMaxPublishedLayer(DefaultMaxLayerSpatial)

	bitrates := Bitrates{
		{1, 2, 3, 4},
		{5, 6, 7, 8},
		{9, 10, 11, 12},
	}

	f.ProvisionalAllocatePrepare(bitrates)
	f.ProvisionalAllocate(bitrates[2][3], VideoLayers{Spatial: 0, Temporal: 0}, true, true)
	// should have set target at (0, 0)
	f.ProvisionalAllocateCommit()

	f.Mute(true)
	expectedResult := VideoAllocation{
		pauseReason:         VideoPauseReasonMuted,
		bandwidthRequested:  0,
		bandwidthDelta:      0 - bitrates[0][0],
		bitrates:            bitrates,
		targetLayers:        InvalidLayers,
		requestLayerSpatial: InvalidLayerSpatial,
		maxLayers:           DefaultMaxLayers,
		distanceToDesired:   0,
	}
	result := f.Pause(bitrates)
	require.Equal(t, expectedResult, result)
	require.Equal(t, expectedResult, f.lastAllocation)
	require.Equal(t, InvalidLayers, f.TargetLayers())
}

func TestForwarderGetTranslationParamsMuted(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)
	f.Mute(true)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, err := testutils.GetTestExtPacket(params)
	require.NoError(t, err)
	require.NotNil(t, extPkt)

	expectedTP := TranslationParams{
		shouldDrop: true,
	}
	actualTP, err := f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)
}

func TestForwarderGetTranslationParamsAudio(t *testing.T) {
	f := newForwarder(testutils.TestOpusCodec, webrtc.RTPCodecTypeAudio)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ := testutils.GetTestExtPacket(params)

	// should lock onto the first packet
	expectedTP := TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23333,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err := f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)
	require.True(t, f.started)
	require.Equal(t, f.lastSSRC, params.SSRC)

	// send a duplicate, should be dropped
	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// out-of-order packet not in cache should be dropped
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23332,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		shouldDrop:         true,
		isDroppingRelevant: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// padding only packet in order should be dropped
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23334,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// in order packet should be forwarded
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23335,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23334,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// padding only packet after a gap should be forwarded
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23337,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingGap,
			sequenceNumber: 23336,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// out-of-order should be forwarded using cache
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23336,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingOutOfOrder,
			sequenceNumber: 23335,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// switching source should lock onto the new source, but sequence number should be contiguous
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 123,
		Timestamp:      0xfedcba,
		SSRC:           0x87654321,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23337,
			timestamp:      0xabcdf0,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)
	require.Equal(t, f.lastSSRC, params.SSRC)
}

func TestForwarderGetTranslationParamsVideo(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 := &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              1,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       false,
	}
	extPkt, _ := testutils.GetTestExtPacketVP8(params, vp8)

	// no target layers, should drop
	expectedTP := TranslationParams{
		shouldDrop: true,
	}
	actualTP, err := f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// although target layer matches, not a key frame, so should drop
	f.targetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// should lock onto packet (target layer and key frame)
	vp8 = &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              1,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		isSwitchingToMaxLayer:    true,
		isSwitchingToTargetLayer: true,
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23333,
			timestamp:      0xabcdef,
		},
		vp8: &TranslationParamsVP8{
			Header: &buffer.VP8{
				FirstByte:        25,
				PictureIDPresent: 1,
				PictureID:        13467,
				MBit:             true,
				TL0PICIDXPresent: 1,
				TL0PICIDX:        233,
				TIDPresent:       1,
				TID:              1,
				Y:                1,
				KEYIDXPresent:    1,
				KEYIDX:           23,
				HeaderSize:       6,
				IsKeyFrame:       true,
			},
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)
	require.True(t, f.started)
	require.Equal(t, f.lastSSRC, params.SSRC)

	// send a duplicate, should be dropped
	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// out-of-order packet not in cache should be dropped
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23332,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		shouldDrop:         true,
		isDroppingRelevant: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// padding only packet in order should be dropped
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23334,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// in order packet should be forwarded
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23335,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23334,
			timestamp:      0xabcdef,
		},
		vp8: &TranslationParamsVP8{
			Header: &buffer.VP8{
				FirstByte:        25,
				PictureIDPresent: 1,
				PictureID:        13467,
				MBit:             true,
				TL0PICIDXPresent: 1,
				TL0PICIDX:        233,
				TIDPresent:       1,
				TID:              1,
				Y:                1,
				KEYIDXPresent:    1,
				KEYIDX:           23,
				HeaderSize:       6,
				IsKeyFrame:       true,
			},
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// temporal layer higher than target, should be dropped
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23336,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 = &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13468,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              2,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		shouldDrop: true,
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// RTP sequence number and VP8 picture id should be contiguous after dropping higher temporal layer picture
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23337,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 = &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13469,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        234,
		TIDPresent:       1,
		TID:              0,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       false,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)
	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23335,
			timestamp:      0xabcdef,
		},
		vp8: &TranslationParamsVP8{
			Header: &buffer.VP8{
				FirstByte:        25,
				PictureIDPresent: 1,
				PictureID:        13468,
				MBit:             true,
				TL0PICIDXPresent: 1,
				TL0PICIDX:        234,
				TIDPresent:       1,
				TID:              0,
				Y:                1,
				KEYIDXPresent:    1,
				KEYIDX:           23,
				HeaderSize:       6,
				IsKeyFrame:       false,
			},
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// padding only packet after a gap should be forwarded
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23339,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingGap,
			sequenceNumber: 23337,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// out-of-order should be forwarded using cache, even if it is padding only
	params = &testutils.TestExtPacketParams{
		SequenceNumber: 23338,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
	}
	extPkt, _ = testutils.GetTestExtPacket(params)

	expectedTP = TranslationParams{
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingOutOfOrder,
			sequenceNumber: 23336,
			timestamp:      0xabcdef,
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 0)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)

	// switching SSRC (happens for new layer or new track source)
	// should lock onto the new source, but sequence number should be contiguous
	f.targetLayers = VideoLayers{
		Spatial:  1,
		Temporal: 1,
	}

	params = &testutils.TestExtPacketParams{
		SequenceNumber: 123,
		Timestamp:      0xfedcba,
		SSRC:           0x87654321,
		PayloadSize:    20,
	}
	vp8 = &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        45,
		MBit:             false,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        12,
		TIDPresent:       1,
		TID:              0,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           30,
		HeaderSize:       5,
		IsKeyFrame:       true,
	}
	extPkt, _ = testutils.GetTestExtPacketVP8(params, vp8)

	expectedTP = TranslationParams{
		isSwitchingToMaxLayer:    true,
		isSwitchingToTargetLayer: true,
		rtp: &TranslationParamsRTP{
			snOrdering:     SequenceNumberOrderingContiguous,
			sequenceNumber: 23338,
			timestamp:      0xabcdf0,
		},
		vp8: &TranslationParamsVP8{
			Header: &buffer.VP8{
				FirstByte:        25,
				PictureIDPresent: 1,
				PictureID:        13469,
				MBit:             true,
				TL0PICIDXPresent: 1,
				TL0PICIDX:        235,
				TIDPresent:       1,
				TID:              0,
				Y:                1,
				KEYIDXPresent:    1,
				KEYIDX:           24,
				HeaderSize:       6,
				IsKeyFrame:       true,
			},
		},
	}
	actualTP, err = f.GetTranslationParams(extPkt, 1)
	require.NoError(t, err)
	require.Equal(t, expectedTP, *actualTP)
	require.Equal(t, f.lastSSRC, params.SSRC)
}

func TestForwardGetSnTsForPadding(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 := &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              13,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	extPkt, _ := testutils.GetTestExtPacketVP8(params, vp8)

	f.targetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	f.currentLayers = InvalidLayers

	// send it through so that forwarder locks onto stream
	_, _ = f.GetTranslationParams(extPkt, 0)

	// pause stream and get padding, it should still work
	disable(f)

	// should get back frame end needed as the last packet did not have RTP marker set
	snts, err := f.GetSnTsForPadding(5)
	require.NoError(t, err)

	numPadding := 5
	clockRate := uint32(0)
	frameRate := uint32(5)
	var sntsExpected = make([]SnTs, numPadding)
	for i := 0; i < numPadding; i++ {
		sntsExpected[i] = SnTs{
			sequenceNumber: 23333 + uint16(i) + 1,
			timestamp:      0xabcdef + (uint32(i)*clockRate)/frameRate,
		}
	}
	require.Equal(t, sntsExpected, snts)

	// now that there is a marker, timestamp should jump on first padding when asked again
	snts, err = f.GetSnTsForPadding(numPadding)
	require.NoError(t, err)

	for i := 0; i < numPadding; i++ {
		sntsExpected[i] = SnTs{
			sequenceNumber: 23338 + uint16(i) + 1,
			timestamp:      0xabcdef + (uint32(i+1)*clockRate)/frameRate,
		}
	}
	require.Equal(t, sntsExpected, snts)
}

func TestForwardGetSnTsForBlankFrames(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 := &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              13,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	extPkt, _ := testutils.GetTestExtPacketVP8(params, vp8)

	f.targetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	f.currentLayers = InvalidLayers

	// send it through so that forwarder locks onto stream
	_, _ = f.GetTranslationParams(extPkt, 0)

	// should get back frame end needed as the last packet did not have RTP marker set
	numBlankFrames := 6
	snts, frameEndNeeded, err := f.GetSnTsForBlankFrames(30, numBlankFrames)
	require.NoError(t, err)
	require.True(t, frameEndNeeded)

	// there should be one more than RTPBlankFramesMax as one would have been allocated to end previous frame
	numPadding := numBlankFrames + 1
	clockRate := testutils.TestVP8Codec.ClockRate
	frameRate := uint32(30)
	var sntsExpected = make([]SnTs, numPadding)
	for i := 0; i < numPadding; i++ {
		sntsExpected[i] = SnTs{
			sequenceNumber: params.SequenceNumber + uint16(i) + 1,
			timestamp:      params.Timestamp + (uint32(i)*clockRate)/frameRate,
		}
	}
	require.Equal(t, sntsExpected, snts)

	// now that there is a marker, timestamp should jump on first padding when asked again
	// also number of padding should be RTPBlankFramesMax
	numPadding = numBlankFrames
	sntsExpected = sntsExpected[:numPadding]
	for i := 0; i < numPadding; i++ {
		sntsExpected[i] = SnTs{
			sequenceNumber: params.SequenceNumber + uint16(len(snts)) + uint16(i) + 1,
			timestamp:      snts[len(snts)-1].timestamp + (uint32(i+1)*clockRate)/frameRate,
		}
	}
	snts, frameEndNeeded, err = f.GetSnTsForBlankFrames(30, numBlankFrames)
	require.NoError(t, err)
	require.False(t, frameEndNeeded)
	require.Equal(t, sntsExpected, snts)
}

func TestForwardGetPaddingVP8(t *testing.T) {
	f := newForwarder(testutils.TestVP8Codec, webrtc.RTPCodecTypeVideo)

	params := &testutils.TestExtPacketParams{
		SequenceNumber: 23333,
		Timestamp:      0xabcdef,
		SSRC:           0x12345678,
		PayloadSize:    20,
	}
	vp8 := &buffer.VP8{
		FirstByte:        25,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              13,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	extPkt, _ := testutils.GetTestExtPacketVP8(params, vp8)

	f.targetLayers = VideoLayers{
		Spatial:  0,
		Temporal: 1,
	}
	f.currentLayers = InvalidLayers

	// send it through so that forwarder locks onto stream
	_, _ = f.GetTranslationParams(extPkt, 0)

	// getting padding with frame end needed, should repeat the last picture id
	expectedVP8 := buffer.VP8{
		FirstByte:        16,
		PictureIDPresent: 1,
		PictureID:        13467,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        233,
		TIDPresent:       1,
		TID:              0,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           23,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	blankVP8 := f.GetPaddingVP8(true)
	require.Equal(t, expectedVP8, *blankVP8)

	// getting padding with no frame end needed, should get next picture id
	expectedVP8 = buffer.VP8{
		FirstByte:        16,
		PictureIDPresent: 1,
		PictureID:        13468,
		MBit:             true,
		TL0PICIDXPresent: 1,
		TL0PICIDX:        234,
		TIDPresent:       1,
		TID:              0,
		Y:                1,
		KEYIDXPresent:    1,
		KEYIDX:           24,
		HeaderSize:       6,
		IsKeyFrame:       true,
	}
	blankVP8 = f.GetPaddingVP8(false)
	require.Equal(t, expectedVP8, *blankVP8)
}
