/*
 * Copyright 2022 LiveKit, Inc
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rtc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v3/pkg/rtcerr"

	"github.com/livekit/livekit-server/pkg/rtc/types"
	"github.com/livekit/livekit-server/pkg/sfu"
	"github.com/livekit/livekit-server/pkg/telemetry"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
)

// using var instead of const to override in tests
var (
	reconcileInterval = 3 * time.Second
	// amount of time to give up if a track or publisher isn't found
	notFoundTimeout = 5 * time.Second
	// amount of time to try otherwise before flagging subscription as failed
	subscriptionTimeout = 10 * time.Second
)

type SubscriptionManagerParams struct {
	Logger              logger.Logger
	Participant         types.LocalParticipant
	TrackResolver       types.MediaTrackResolver
	OnTrackSubscribed   func(subTrack types.SubscribedTrack)
	OnTrackUnsubscribed func(subTrack types.SubscribedTrack)
	OnSubcriptionError  func(trackID livekit.TrackID)
	Telemetry           telemetry.TelemetryService
}

// SubscriptionManager manages a participant's subscriptions
type SubscriptionManager struct {
	params        SubscriptionManagerParams
	lock          sync.RWMutex
	subscriptions map[livekit.TrackID]*trackSubscription
	subscribedTo  map[livekit.ParticipantID]map[livekit.TrackID]struct{}
	reconcileCh   chan livekit.TrackID
	closeCh       chan struct{}
	doneCh        chan struct{}

	onSubscribeStatusChanged func(publisherID livekit.ParticipantID, subscribed bool)
}

func NewSubscriptionManager(params SubscriptionManagerParams) *SubscriptionManager {
	m := &SubscriptionManager{
		params:        params,
		subscriptions: make(map[livekit.TrackID]*trackSubscription),
		subscribedTo:  make(map[livekit.ParticipantID]map[livekit.TrackID]struct{}),
		reconcileCh:   make(chan livekit.TrackID, 10),
		closeCh:       make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go m.reconcileWorker()
	return m
}

func (m *SubscriptionManager) Close(willBeResumed bool) {
	m.lock.Lock()
	if m.IsClosed() {
		m.lock.Unlock()
		return
	}
	close(m.closeCh)
	m.lock.Unlock()

	<-m.doneCh

	subTracks := m.GetSubscribedTracks()
	downTracksToClose := make([]*sfu.DownTrack, 0, len(subTracks))
	for _, st := range subTracks {
		dt := st.DownTrack()
		// nil check exists primarily for tests
		if dt != nil {
			downTracksToClose = append(downTracksToClose, st.DownTrack())
		}
	}

	go func() {
		for _, dt := range downTracksToClose {
			dt.CloseWithFlush(!willBeResumed)
		}
	}()
}

func (m *SubscriptionManager) IsClosed() bool {
	select {
	case <-m.closeCh:
		return true
	default:
		return false
	}
}

func (m *SubscriptionManager) SubscribeToTrack(trackID livekit.TrackID, publisherIdentity livekit.ParticipantIdentity, publisherID livekit.ParticipantID) {
	m.lock.Lock()
	sub, ok := m.subscriptions[trackID]
	if !ok {
		sub = newTrackSubscription(trackID)
		m.subscriptions[trackID] = sub
	}
	m.lock.Unlock()
	sub.setPublisher(publisherIdentity, publisherID)
	if sub.setDesired(true) {
		m.params.Logger.Infow("subscribing to track",
			"trackID", trackID,
			"publisherID", publisherID,
			"publisherIdentity", publisherIdentity,
		)
		m.queueReconcile(trackID)
	}
}

func (m *SubscriptionManager) UnsubscribeFromTrack(trackID livekit.TrackID) {
	m.lock.Lock()
	sub, ok := m.subscriptions[trackID]
	m.lock.Unlock()
	if !ok {
		return
	}

	if sub.setDesired(false) {
		m.params.Logger.Infow("unsubscribing from track",
			"trackID", trackID,
			"publisherID", sub.getPublisherID(),
			"publisherIdentity", sub.getPublisherIdentity(),
		)
		m.queueReconcile(trackID)
	}
}

func (m *SubscriptionManager) GetSubscribedTracks() []types.SubscribedTrack {
	m.lock.RLock()
	defer m.lock.RUnlock()

	tracks := make([]types.SubscribedTrack, 0, len(m.subscriptions))
	for _, t := range m.subscriptions {
		st := t.getSubscribedTrack()
		if st != nil {
			tracks = append(tracks, st)
		}
	}
	return tracks
}

func (m *SubscriptionManager) HasSubscriptions() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	for _, s := range m.subscriptions {
		if s.isDesired() {
			return true
		}
	}
	return false
}

func (m *SubscriptionManager) GetSubscribedParticipants() []livekit.ParticipantID {
	m.lock.RLock()
	defer m.lock.RUnlock()

	var participantIDs []livekit.ParticipantID
	for pID := range m.subscribedTo {
		participantIDs = append(participantIDs, pID)
	}
	return participantIDs
}

func (m *SubscriptionManager) IsSubscribedTo(participantID livekit.ParticipantID) bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	_, ok := m.subscribedTo[participantID]
	return ok
}

func (m *SubscriptionManager) UpdateSubscribedTrackSettings(trackID livekit.TrackID, settings *livekit.UpdateTrackSettings) {
	m.lock.Lock()
	sub, ok := m.subscriptions[trackID]
	if !ok {
		sub = newTrackSubscription(trackID)
		m.subscriptions[trackID] = sub
	}
	m.lock.Unlock()

	sub.setSettings(settings)
}

// OnSubscribeStatusChanged callback will be notified when a participant subscribes or unsubscribes to another participant
// it will only fire once per publisher. If current participant is subscribed to multiple tracks from another, this
// callback will only fire once.
func (m *SubscriptionManager) OnSubscribeStatusChanged(fn func(publisherID livekit.ParticipantID, subscribed bool)) {
	m.lock.Lock()
	m.onSubscribeStatusChanged = fn
	m.lock.Unlock()
}

func (m *SubscriptionManager) canReconcile() bool {
	p := m.params.Participant
	if m.IsClosed() || p.IsClosed() || p.IsDisconnected() {
		return false
	}
	return true
}

func (m *SubscriptionManager) reconcileSubscriptions() {
	var needsToReconcile []*trackSubscription
	m.lock.RLock()
	for _, sub := range m.subscriptions {
		if sub.needsSubscribe() || sub.needsUnsubscribe() || sub.needsBind() {
			needsToReconcile = append(needsToReconcile, sub)
		}
	}
	m.lock.RUnlock()

	for _, s := range needsToReconcile {
		m.reconcileSubscription(s)
	}
}

func (m *SubscriptionManager) reconcileSubscription(s *trackSubscription) {
	if !m.canReconcile() {
		return
	}
	if s.needsSubscribe() {
		if s.numAttempts.Load() == 0 {
			m.params.Telemetry.TrackSubscribeRequested(
				context.Background(),
				m.params.Participant.ID(),
				&livekit.TrackInfo{
					Sid: string(s.trackID),
				},
				&livekit.ParticipantInfo{
					Sid:      string(s.getPublisherID()),
					Identity: string(s.getPublisherIdentity()),
				},
			)
		}
		if err := m.subscribe(s); err != nil {
			s.recordAttempt(false)

			switch err {
			case ErrNoTrackPermission, ErrNoSubscribePermission, ErrTrackNotAttached:
				// ignore permission errors, it's outside of our control and publisher could
				// grant any time
			case ErrPublisherNotConnected, ErrTrackNotFound:
				// publisher left or track was unpublished, if after timeout, we'd unsubscribe
				// from it. this is the *only* case we'd change desired state
				if s.durationSinceStart() > notFoundTimeout {
					m.params.Logger.Infow("unsubscribing track since track isn't available",
						"trackID", s.trackID,
						"publisherID", s.getPublisherID(),
						"publisherIdentity", s.getPublisherIdentity(),
					)
					s.setDesired(false)
					m.queueReconcile(s.trackID)
				}
			default:
				// all other errors
				m.params.Logger.Warnw("failed to subscribe", err,
					"attempt", s.numAttempts.Load(),
					"trackID", s.trackID,
				)
				if s.durationSinceStart() > subscriptionTimeout {
					m.params.OnSubcriptionError(s.trackID)
				}
			}
		} else {
			s.recordAttempt(true)
		}

		return
	}

	if s.needsUnsubscribe() {
		if err := m.unsubscribe(s); err != nil {
			m.params.Logger.Errorw("failed to unsubscribe", err,
				"trackID", s.trackID,
			)
		} else {
			// successfully unsubscribed, remove from map
			m.lock.Lock()
			if !s.isDesired() {
				delete(m.subscriptions, s.trackID)
			}
			m.lock.Unlock()
		}
		return
	}

	if s.needsBind() {
		// check bound status, notify error callback if it's not bound
		if s.durationSinceStart() > subscriptionTimeout {
			m.params.Logger.Errorw("track not bound after timeout", nil,
				"trackID", s.trackID,
				"publisherID", s.getPublisherID(),
				"publisherIdentity", s.getPublisherIdentity(),
			)
			m.params.OnSubcriptionError(s.trackID)
		}
	}
}

// trigger an immediate reconcilation, when trackID is empty, will reconcile all subscriptions
func (m *SubscriptionManager) queueReconcile(trackID livekit.TrackID) {
	select {
	case m.reconcileCh <- trackID:
	default:
		// queue is full, will reconcile based on timer
	}
}

func (m *SubscriptionManager) reconcileWorker() {
	reconcileTicker := time.NewTicker(reconcileInterval)
	defer reconcileTicker.Stop()
	defer close(m.doneCh)

	for {
		select {
		case <-m.closeCh:
			return
		case <-reconcileTicker.C:
			m.reconcileSubscriptions()
		case trackID := <-m.reconcileCh:
			m.lock.RLock()
			s := m.subscriptions[trackID]
			m.lock.RUnlock()
			if s != nil {
				m.reconcileSubscription(s)
			} else {
				m.reconcileSubscriptions()
			}
		}
	}
}

func (m *SubscriptionManager) subscribe(s *trackSubscription) error {
	s.startAttempt()

	if !m.params.Participant.CanSubscribe() {
		return ErrNoSubscribePermission
	}

	res, err := m.params.TrackResolver(m.params.Participant.Identity(), s.publisherID, s.trackID)
	if err != nil {
		// TODO: handle participant and track missing errors
		// these scenarios should be the only conditions flipping desired flag to false
		return err
	}

	track := res.Track
	permChanged := s.setHasPermission(res.HasPermission)
	if permChanged {
		m.params.Participant.SubscriptionPermissionUpdate(s.getPublisherID(), s.trackID, res.HasPermission)
	}
	if !res.HasPermission {
		if permChanged {
			track.AddChangeObserver(m.params.Participant.ID(), func() {
				m.queueReconcile(s.trackID)
			})
		}
		return ErrNoTrackPermission
	}
	subTrack, err := track.AddSubscriber(m.params.Participant)
	if err != nil && err != errAlreadySubscribed {
		// ignore already subscribed error
		return err
	}
	subTrack.OnClose(func(willBeResumed bool) {
		m.handleSubscribedTrackClose(s, willBeResumed)
	})
	subTrack.AddOnBind(func() {
		s.setBound()
		m.params.Telemetry.TrackSubscribed(context.Background(), m.params.Participant.ID(), track.ToProto(), &livekit.ParticipantInfo{
			Identity: string(subTrack.PublisherIdentity()),
			Sid:      string(subTrack.PublisherID()),
		})
	})
	s.setSubscribedTrack(subTrack)

	if subTrack.NeedsNegotiation() {
		m.params.Participant.Negotiate(false)
	}

	// add mark the participant as someone we've subscribed to
	firstSubscribe := false
	publisherID := s.getPublisherID()
	m.lock.Lock()
	pTracks := m.subscribedTo[publisherID]
	changedCB := m.onSubscribeStatusChanged
	if pTracks == nil {
		pTracks = make(map[livekit.TrackID]struct{})
		m.subscribedTo[publisherID] = pTracks
		firstSubscribe = true
	}
	pTracks[s.trackID] = struct{}{}
	m.lock.Unlock()

	go m.params.OnTrackSubscribed(subTrack)

	if changedCB != nil && firstSubscribe {
		go changedCB(publisherID, true)
	}
	return nil
}

func (m *SubscriptionManager) unsubscribe(s *trackSubscription) error {
	// remove from subscribedTo
	subTrack := s.getSubscribedTrack()
	if subTrack == nil {
		// already unsubscribed
		return nil
	}

	track := subTrack.MediaTrack()
	pID := m.params.Participant.ID()
	track.RemoveChangeObserver(pID)
	track.RemoveSubscriber(pID, false)

	return nil
}

// DownTrack closing is how the publisher signifies that the subscription is no longer fulfilled
// this could be due to a few reasons:
// - subscriber-initiated unsubscribe
// - UpTrack was closed
// - publisher revoked permissions for the participant
func (m *SubscriptionManager) handleSubscribedTrackClose(s *trackSubscription, willBeResumed bool) {
	m.params.Logger.Debugw("subscribed track closed",
		"trackID", s.trackID,
		"publisherID", s.getPublisherID(),
		"publisherIdentity", s.getPublisherIdentity(),
		"willBeResumed", willBeResumed,
	)
	subTrack := s.getSubscribedTrack()
	if subTrack == nil {
		return
	}

	// remove from subscribedTo
	publisherID := s.getPublisherID()
	lastSubscription := false
	m.lock.Lock()
	changedCB := m.onSubscribeStatusChanged
	pTracks := m.subscribedTo[publisherID]
	if pTracks != nil {
		delete(pTracks, s.trackID)
		if len(pTracks) == 0 {
			delete(m.subscribedTo, publisherID)
			lastSubscription = true
		}
	}
	m.lock.Unlock()
	if changedCB != nil && lastSubscription {
		go changedCB(publisherID, false)
	}

	subTrack.OnClose(nil)
	s.setSubscribedTrack(nil)
	go m.params.OnTrackUnsubscribed(subTrack)

	if !willBeResumed {
		if !m.params.Participant.IsClosed() {
			m.params.Telemetry.TrackUnsubscribed(context.Background(), m.params.Participant.ID(), subTrack.MediaTrack().ToProto())
		}

		sender := subTrack.RTPSender()
		if sender != nil {
			m.params.Logger.Debugw("removing PeerConnection track",
				"publisher", subTrack.PublisherIdentity(),
				"publisherID", subTrack.PublisherID(),
				"kind", subTrack.MediaTrack().Kind(),
			)

			if err := m.params.Participant.RemoveTrackFromSubscriber(sender); err != nil {
				if _, ok := err.(*rtcerr.InvalidStateError); !ok {
					// most of these are safe to ignore, since the track state might have already
					// been set to Inactive
					m.params.Logger.Debugw("could not remove remoteTrack from forwarder",
						"error", err,
						"publisher", subTrack.PublisherIdentity(),
						"publisherID", subTrack.PublisherID(),
					)
				}
			}
		}
	}

	if !willBeResumed {
		m.params.Participant.Negotiate(false)
	}
	m.queueReconcile(s.trackID)
}

type trackSubscription struct {
	lock              sync.RWMutex
	desired           bool
	trackID           livekit.TrackID
	publisherID       livekit.ParticipantID
	publisherIdentity livekit.ParticipantIdentity
	settings          *livekit.UpdateTrackSettings
	hasPermission     bool
	subscribedTrack   types.SubscribedTrack
	numAttempts       atomic.Int32
	bound             bool
	subStartedAt      atomic.Pointer[time.Time]
}

func newTrackSubscription(trackID livekit.TrackID) *trackSubscription {
	return &trackSubscription{
		trackID: trackID,
		// default allow
		hasPermission: true,
	}
}

func (s *trackSubscription) setPublisher(publisherIdentity livekit.ParticipantIdentity, publisherID livekit.ParticipantID) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.publisherID = publisherID
	s.publisherIdentity = publisherIdentity
}

func (s *trackSubscription) getPublisherID() livekit.ParticipantID {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.publisherID
}

func (s *trackSubscription) getPublisherIdentity() livekit.ParticipantIdentity {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.publisherIdentity
}

func (s *trackSubscription) setDesired(desired bool) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.desired == desired {
		return false
	}
	s.desired = desired
	return true
}

// set permission and return true if it has changed
func (s *trackSubscription) setHasPermission(perm bool) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.hasPermission == perm {
		return false
	}
	s.hasPermission = perm
	return true
}

func (s *trackSubscription) getHasPermission() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.hasPermission
}

func (s *trackSubscription) isDesired() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.desired
}

func (s *trackSubscription) setSubscribedTrack(track types.SubscribedTrack) {
	s.lock.Lock()
	s.subscribedTrack = track
	s.bound = false
	settings := s.settings
	s.lock.Unlock()

	if settings != nil && track != nil {
		track.UpdateSubscriberSettings(settings)
	}
}

func (s *trackSubscription) getSubscribedTrack() types.SubscribedTrack {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.subscribedTrack
}

func (s *trackSubscription) setSettings(settings *livekit.UpdateTrackSettings) {
	s.lock.Lock()
	s.settings = settings
	subTrack := s.subscribedTrack
	s.lock.Unlock()
	if subTrack != nil {
		subTrack.UpdateSubscriberSettings(settings)
	}
}

// mark the subscription as bound - when we've received the client's answer
func (s *trackSubscription) setBound() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.bound = true
}

func (s *trackSubscription) startAttempt() {
	if s.numAttempts.Load() == 0 {
		t := time.Now()
		s.subStartedAt.Store(&t)
	}
}

func (s *trackSubscription) recordAttempt(success bool) {
	if !success {
		s.numAttempts.Add(1)
	} else {
		s.numAttempts.Store(0)
	}
}

func (s *trackSubscription) durationSinceStart() time.Duration {
	t := s.subStartedAt.Load()
	if t == nil {
		return 0
	}
	return time.Since(*t)
}

func (s *trackSubscription) needsSubscribe() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.desired && s.subscribedTrack == nil
}

func (s *trackSubscription) needsUnsubscribe() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return !s.desired && s.subscribedTrack != nil
}

func (s *trackSubscription) needsBind() bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.desired && s.subscribedTrack != nil && !s.bound
}
