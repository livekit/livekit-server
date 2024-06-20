// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"
	"github.com/thoas/go-funk"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/livekit/livekit-server/pkg/config"
	"github.com/livekit/livekit-server/pkg/rtc"
	"github.com/livekit/livekit-server/pkg/testutils"
	testclient "github.com/livekit/livekit-server/test/client"
)

const (
	waitTick    = 10 * time.Millisecond
	waitTimeout = 5 * time.Second
)

func TestClientCouldConnect(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestClientCouldConnect")
	defer finish()

	c1 := createRTCClient("c1", defaultServerPort, nil)
	c2 := createRTCClient("c2", defaultServerPort, nil)
	waitUntilConnected(t, c1, c2)

	// ensure they both see each other
	testutils.WithTimeout(t, func() string {
		if len(c1.RemoteParticipants()) == 0 {
			return "c1 did not see c2"
		}
		if len(c2.RemoteParticipants()) == 0 {
			return "c2 did not see c1"
		}
		return ""
	})
}

func TestClientConnectDuplicate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestClientCouldConnect")
	defer finish()

	grant := &auth.VideoGrant{RoomJoin: true, Room: testRoom}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	token := joinTokenWithGrant("c1", grant)

	c1 := createRTCClientWithToken(token, defaultServerPort, nil)

	// publish 2 tracks
	t1, err := c1.AddStaticTrack("audio/opus", "audio", "webcam")
	require.NoError(t, err)
	defer t1.Stop()
	t2, err := c1.AddStaticTrack("video/vp8", "video", "webcam")
	require.NoError(t, err)
	defer t2.Stop()

	c2 := createRTCClient("c2", defaultServerPort, nil)
	waitUntilConnected(t, c1, c2)

	opts := &testclient.Options{
		Publish: "duplicate_connection",
	}
	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2 didn't subscribe to anything"
		}
		// should have received three tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 2 {
			return "c2 didn't subscribe to both tracks from c1"
		}

		// participant ID can be appended with '#..' . but should contain orig id as prefix
		tr1 := c2.SubscribedTracks()[c1.ID()][0]
		participantId1, _ := rtc.UnpackStreamID(tr1.StreamID())
		require.Equal(t, c1.ID(), participantId1)
		tr2 := c2.SubscribedTracks()[c1.ID()][1]
		participantId2, _ := rtc.UnpackStreamID(tr2.StreamID())
		require.Equal(t, c1.ID(), participantId2)
		return ""
	})

	c1Dup := createRTCClientWithToken(token, defaultServerPort, opts)

	waitUntilConnected(t, c1Dup)

	t3, err := c1Dup.AddStaticTrack("video/vp8", "video", "webcam")
	require.NoError(t, err)
	defer t3.Stop()

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()[c1Dup.ID()]) != 1 {
			return "c2 was not subscribed to track from duplicated c1"
		}

		tr3 := c2.SubscribedTracks()[c1Dup.ID()][0]
		participantId3, _ := rtc.UnpackStreamID(tr3.StreamID())
		require.Contains(t, c1Dup.ID(), participantId3)

		return ""
	})
}

func TestSinglePublisher(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	s, finish := setupSingleNodeTest("TestSinglePublisher")
	defer finish()

	c1 := createRTCClient("c1", defaultServerPort, nil)
	c2 := createRTCClient("c2", defaultServerPort, nil)
	waitUntilConnected(t, c1, c2)

	// publish a track and ensure clients receive it ok
	t1, err := c1.AddStaticTrack("audio/opus", "audio", "webcamaudio")
	require.NoError(t, err)
	defer t1.Stop()
	t2, err := c1.AddStaticTrack("video/vp8", "video", "webcamvideo")
	require.NoError(t, err)
	defer t2.Stop()

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2 was not subscribed to anything"
		}
		// should have received two tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 2 {
			return "c2 didn't subscribe to both tracks from c1"
		}

		tr1 := c2.SubscribedTracks()[c1.ID()][0]
		participantId, _ := rtc.UnpackStreamID(tr1.StreamID())
		require.Equal(t, c1.ID(), participantId)
		return ""
	})
	// ensure mime type is received
	remoteC1 := c2.GetRemoteParticipant(c1.ID())
	audioTrack := funk.Find(remoteC1.Tracks, func(ti *livekit.TrackInfo) bool {
		return ti.Name == "webcamaudio"
	}).(*livekit.TrackInfo)
	require.Equal(t, "audio/opus", audioTrack.MimeType)

	// a new client joins and should get the initial stream
	c3 := createRTCClient("c3", defaultServerPort, nil)

	// ensure that new client that has joined also received tracks
	waitUntilConnected(t, c3)
	testutils.WithTimeout(t, func() string {
		if len(c3.SubscribedTracks()) == 0 {
			return "c3 didn't subscribe to anything"
		}
		// should have received two tracks
		if len(c3.SubscribedTracks()[c1.ID()]) != 2 {
			return "c3 didn't subscribe to tracks from c1"
		}
		return ""
	})

	// ensure that the track ids are generated by server
	tracks := c3.SubscribedTracks()[c1.ID()]
	for _, tr := range tracks {
		require.True(t, strings.HasPrefix(tr.ID(), "TR_"), "track should begin with TR")
	}

	// when c3 disconnects, ensure subscriber is cleaned up correctly
	c3.Stop()

	testutils.WithTimeout(t, func() string {
		room := s.RoomManager().GetRoom(context.Background(), testRoom)
		p := room.GetParticipant("c1")
		require.NotNil(t, p)

		for _, t := range p.GetPublishedTracks() {
			if t.IsSubscriber(c3.ID()) {
				return "c3 was not a subscriber of c1's tracks"
			}
		}
		return ""
	})
}

func Test_WhenAutoSubscriptionDisabled_ClientShouldNotReceiveAnyPublishedTracks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("Test_WhenAutoSubscriptionDisabled_ClientShouldNotReceiveAnyPublishedTracks")
	defer finish()

	opts := testclient.Options{AutoSubscribe: false}
	publisher := createRTCClient("publisher", defaultServerPort, &opts)
	client := createRTCClient("client", defaultServerPort, &opts)
	defer publisher.Stop()
	defer client.Stop()
	waitUntilConnected(t, publisher, client)

	track, err := publisher.AddStaticTrack("audio/opus", "audio", "webcam")
	require.NoError(t, err)
	defer track.Stop()

	time.Sleep(syncDelay)

	require.Empty(t, client.SubscribedTracks()[publisher.ID()])
}

func Test_RenegotiationWithDifferentCodecs(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestRenegotiationWithDifferentCodecs")
	defer finish()

	c1 := createRTCClient("c1", defaultServerPort, nil)
	c2 := createRTCClient("c2", defaultServerPort, nil)
	waitUntilConnected(t, c1, c2)

	// publish a vp8 video track and ensure clients receive it ok
	t1, err := c1.AddStaticTrack("audio/opus", "audio", "webcam")
	require.NoError(t, err)
	defer t1.Stop()
	t2, err := c1.AddStaticTrack("video/vp8", "video", "webcam")
	require.NoError(t, err)
	defer t2.Stop()

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2 was not subscribed to anything"
		}
		// should have received two tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 2 {
			return "c2 was not subscribed to tracks from c1"
		}

		tracks := c2.SubscribedTracks()[c1.ID()]
		for _, t := range tracks {
			if strings.EqualFold(t.Codec().MimeType, "video/vp8") {
				return ""

			}
		}
		return "did not receive track with vp8"
	})

	t3, err := c1.AddStaticTrackWithCodec(webrtc.RTPCodecCapability{
		MimeType:    "video/h264",
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "videoscreen", "screen")
	defer t3.Stop()
	require.NoError(t, err)

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2's not subscribed to anything"
		}
		// should have received three tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 3 {
			return "c2's not subscribed to 3 tracks from c1"
		}

		var vp8Found, h264Found bool
		tracks := c2.SubscribedTracks()[c1.ID()]
		for _, t := range tracks {
			if strings.EqualFold(t.Codec().MimeType, "video/vp8") {
				vp8Found = true
			} else if strings.EqualFold(t.Codec().MimeType, "video/h264") {
				h264Found = true
			}
		}
		if !vp8Found {
			return "did not receive track with vp8"
		}
		if !h264Found {
			return "did not receive track with h264"
		}
		return ""
	})
}

func TestSingleNodeRoomList(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}
	_, finish := setupSingleNodeTest("TestSingleNodeRoomList")
	defer finish()

	roomServiceListRoom(t)
}

// Ensure that CORS headers are returned
func TestSingleNodeCORS(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}
	s, finish := setupSingleNodeTest("TestSingleNodeCORS")
	defer finish()

	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d", s.HTTPPort()), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "bearer xyz")
	req.Header.Set("Origin", "testhost.com")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, "testhost.com", res.Header.Get("Access-Control-Allow-Origin"))
}

func TestPingPong(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}
	_, finish := setupSingleNodeTest("TestPingPong")
	defer finish()

	c1 := createRTCClient("c1", defaultServerPort, nil)
	waitUntilConnected(t, c1)

	require.NoError(t, c1.SendPing())
	require.Eventually(t, func() bool {
		return c1.PongReceivedAt() > 0
	}, time.Second, 10*time.Millisecond)
}

func TestSingleNodeJoinAfterClose(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestJoinAfterClose")
	defer finish()

	scenarioJoinClosedRoom(t)
}

func TestSingleNodeCloseNonRTCRoom(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("closeNonRTCRoom")
	defer finish()

	closeNonRTCRoom(t)
}

func TestAutoCreate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}
	disableAutoCreate := func(conf *config.Config) {
		conf.Room.AutoCreate = false
	}
	t.Run("cannot join if room isn't created", func(t *testing.T) {
		s := createSingleNodeServer(disableAutoCreate)
		go func() {
			if err := s.Start(); err != nil {
				logger.Errorw("server returned error", err)
			}
		}()
		defer s.Stop(true)

		waitForServerToStart(s)

		token := joinToken(testRoom, "start-before-create", nil)
		_, err := testclient.NewWebSocketConn(fmt.Sprintf("ws://localhost:%d", defaultServerPort), token, nil)
		require.Error(t, err)

		// second join should also fail
		token = joinToken(testRoom, "start-before-create-2", nil)
		_, err = testclient.NewWebSocketConn(fmt.Sprintf("ws://localhost:%d", defaultServerPort), token, nil)
		require.Error(t, err)
	})

	t.Run("join with explicit createRoom", func(t *testing.T) {
		s := createSingleNodeServer(disableAutoCreate)
		go func() {
			if err := s.Start(); err != nil {
				logger.Errorw("server returned error", err)
			}
		}()
		defer s.Stop(true)

		waitForServerToStart(s)

		// explicitly create
		_, err := roomClient.CreateRoom(contextWithToken(createRoomToken()), &livekit.CreateRoomRequest{Name: testRoom})
		require.NoError(t, err)

		c1 := createRTCClient("join-after-create", defaultServerPort, nil)
		waitUntilConnected(t, c1)

		c1.Stop()
	})
}

// don't give user subscribe permissions initially, and ensure autosubscribe is triggered afterwards
func TestSingleNodeUpdateSubscriptionPermissions(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}
	_, finish := setupSingleNodeTest("TestSingleNodeUpdateSubscriptionPermissions")
	defer finish()

	pub := createRTCClient("pub", defaultServerPort, nil)
	grant := &auth.VideoGrant{RoomJoin: true, Room: testRoom}
	grant.SetCanSubscribe(false)
	at := auth.NewAccessToken(testApiKey, testApiSecret).
		AddGrant(grant).
		SetIdentity("sub")
	token, err := at.ToJWT()
	require.NoError(t, err)
	sub := createRTCClientWithToken(token, defaultServerPort, nil)

	waitUntilConnected(t, pub, sub)

	writers := publishTracksForClients(t, pub)
	defer stopWriters(writers...)

	// wait sub receives tracks
	testutils.WithTimeout(t, func() string {
		pubRemote := sub.GetRemoteParticipant(pub.ID())
		if pubRemote == nil {
			return "could not find remote publisher"
		}
		if len(pubRemote.Tracks) != 2 {
			return "did not receive metadata for published tracks"
		}
		return ""
	})

	// set permissions out of band
	ctx := contextWithToken(adminRoomToken(testRoom))
	_, err = roomClient.UpdateParticipant(ctx, &livekit.UpdateParticipantRequest{
		Room:     testRoom,
		Identity: "sub",
		Permission: &livekit.ParticipantPermission{
			CanSubscribe: true,
			CanPublish:   true,
		},
	})
	require.NoError(t, err)

	testutils.WithTimeout(t, func() string {
		tracks := sub.SubscribedTracks()[pub.ID()]
		if len(tracks) == 2 {
			return ""
		} else {
			return fmt.Sprintf("expected 2 tracks subscribed, actual: %d", len(tracks))
		}
	})
}

// TestDeviceCodecOverride checks that codecs that are incompatible with a device is not
// negotiated by the server
func TestDeviceCodecOverride(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestDeviceCodecOverride")
	defer finish()

	// simulate device that isn't compatible with H.264
	c1 := createRTCClient("c1", defaultServerPort, &testclient.Options{
		ClientInfo: &livekit.ClientInfo{
			Os:          "android",
			DeviceModel: "Xiaomi 2201117TI",
		},
	})
	defer c1.Stop()
	waitUntilConnected(t, c1)

	// it doesn't really matter what the codec set here is, uses default Pion MediaEngine codecs
	tw, err := c1.AddStaticTrack("video/h264", "video", "webcam")
	require.NoError(t, err)
	defer stopWriters(tw)

	// wait for server to receive track
	require.Eventually(t, func() bool {
		return c1.LastAnswer() != nil
	}, waitTimeout, waitTick, "did not receive answer")

	sd := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  c1.LastAnswer().SDP,
	}
	answer, err := sd.Unmarshal()
	require.NoError(t, err)

	// video and data channel
	require.Len(t, answer.MediaDescriptions, 2)
	var desc *sdp.MediaDescription
	for _, md := range answer.MediaDescriptions {
		if md.MediaName.Media == "video" {
			desc = md
			break
		}
	}
	require.NotNil(t, desc)
	hasSeenVP8 := false
	for _, a := range desc.Attributes {
		if a.Key == "rtpmap" {
			require.NotContains(t, a.Value, "H264", "should not contain H264 codec")
			if strings.Contains(a.Value, "VP8") {
				hasSeenVP8 = true
			}
		}
	}
	require.True(t, hasSeenVP8, "should have seen VP8 codec in SDP")
}

func TestSubscribeToCodecUnsupported(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	_, finish := setupSingleNodeTest("TestSubscribeToCodecUnsupported")
	defer finish()

	c1 := createRTCClient("c1", defaultServerPort, nil)
	// create a client that doesn't support H264
	c2 := createRTCClient("c2", defaultServerPort, &testclient.Options{
		AutoSubscribe: true,
		DisabledCodecs: []webrtc.RTPCodecCapability{
			{MimeType: "video/H264"},
		},
	})
	waitUntilConnected(t, c1, c2)

	// publish a vp8 video track and ensure c2 receives it ok
	t1, err := c1.AddStaticTrack("audio/opus", "audio", "webcam")
	require.NoError(t, err)
	defer t1.Stop()
	t2, err := c1.AddStaticTrack("video/vp8", "video", "webcam")
	require.NoError(t, err)
	defer t2.Stop()

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2 was not subscribed to anything"
		}
		// should have received two tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 2 {
			return "c2 was not subscribed to tracks from c1"
		}

		tracks := c2.SubscribedTracks()[c1.ID()]
		for _, t := range tracks {
			if strings.EqualFold(t.Codec().MimeType, "video/vp8") {
				return ""
			}
		}
		return "did not receive track with vp8"
	})
	require.Nil(t, c2.GetSubscriptionResponseAndClear())

	// publish a h264 track and ensure c2 got subscription error
	t3, err := c1.AddStaticTrackWithCodec(webrtc.RTPCodecCapability{
		MimeType:    "video/h264",
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "videoscreen", "screen")
	defer t3.Stop()
	require.NoError(t, err)

	var h264TrackID string
	require.Eventually(t, func() bool {
		remoteC1 := c2.GetRemoteParticipant(c1.ID())
		require.NotNil(t, remoteC1)
		for _, track := range remoteC1.Tracks {
			if strings.EqualFold(track.MimeType, "video/h264") {
				h264TrackID = track.Sid
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "did not receive track info with h264")

	require.Eventually(t, func() bool {
		sr := c2.GetSubscriptionResponseAndClear()
		if sr == nil {
			return false
		}
		require.Equal(t, h264TrackID, sr.TrackSid)
		require.Equal(t, livekit.SubscriptionError_SE_CODEC_UNSUPPORTED, sr.Err)
		return true
	}, 5*time.Second, 10*time.Millisecond, "did not receive subscription response")

	// publish another vp8 track again, ensure the transport recovered by sfu and c2 can receive it
	t4, err := c1.AddStaticTrack("video/vp8", "video2", "webcam2")
	require.NoError(t, err)
	defer t4.Stop()

	testutils.WithTimeout(t, func() string {
		if len(c2.SubscribedTracks()) == 0 {
			return "c2 was not subscribed to anything"
		}
		// should have received two tracks
		if len(c2.SubscribedTracks()[c1.ID()]) != 3 {
			return "c2 was not subscribed to tracks from c1"
		}

		var vp8Count int
		tracks := c2.SubscribedTracks()[c1.ID()]
		for _, t := range tracks {
			if strings.EqualFold(t.Codec().MimeType, "video/vp8") {
				vp8Count++
			}
		}
		if vp8Count == 2 {
			return ""
		}
		return "did not 2 receive track with vp8"
	})
	require.Nil(t, c2.GetSubscriptionResponseAndClear())
}
