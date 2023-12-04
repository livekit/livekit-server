/*
 * Copyright 2023 LiveKit, Inc
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

package types

import (
	"strings"
	"sync"

	"github.com/pion/ice/v2"
	"github.com/pion/webrtc/v3"
	"golang.org/x/exp/slices"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
)

type ICEConnectionType string

const (
	ICEConnectionTypeUDP     ICEConnectionType = "udp"
	ICEConnectionTypeTCP     ICEConnectionType = "tcp"
	ICEConnectionTypeTURN    ICEConnectionType = "turn"
	ICEConnectionTypeUnknown ICEConnectionType = "unknown"
)

type ICECandidateExtended struct {
	// only one of local or remote is set. This is due to type foo in Pion
	Local    *webrtc.ICECandidate
	Remote   ice.Candidate
	Selected bool
	Filtered bool
}

type ICEConnectionDetails struct {
	Local     []*ICECandidateExtended
	Remote    []*ICECandidateExtended
	Transport livekit.SignalTarget
	Type      ICEConnectionType
	lock      sync.Mutex
	logger    logger.Logger
}

func NewICEConnectionDetails(transport livekit.SignalTarget, l logger.Logger) *ICEConnectionDetails {
	d := &ICEConnectionDetails{
		Transport: transport,
		Type:      ICEConnectionTypeUnknown,
		logger:    l,
	}
	return d
}

func (d *ICEConnectionDetails) HasCandidates() bool {
	d.lock.Lock()
	defer d.lock.Unlock()
	return len(d.Local) > 0 || len(d.Remote) > 0
}

// Clone returns a copy of the ICEConnectionDetails, where fields can be read without locking
func (d *ICEConnectionDetails) Clone() *ICEConnectionDetails {
	d.lock.Lock()
	defer d.lock.Unlock()
	clone := &ICEConnectionDetails{
		Transport: d.Transport,
		Type:      d.Type,
		logger:    d.logger,
		Local:     make([]*ICECandidateExtended, 0, len(d.Local)),
		Remote:    make([]*ICECandidateExtended, 0, len(d.Remote)),
	}
	for _, c := range d.Local {
		clone.Local = append(clone.Local, &ICECandidateExtended{
			Local:    c.Local,
			Filtered: c.Filtered,
			Selected: c.Selected,
		})
	}
	for _, c := range d.Remote {
		clone.Remote = append(clone.Remote, &ICECandidateExtended{
			Remote:   c.Remote,
			Filtered: c.Filtered,
			Selected: c.Selected,
		})
	}
	return clone
}

func (d *ICEConnectionDetails) AddLocalCandidate(c *webrtc.ICECandidate, filtered bool) {
	d.lock.Lock()
	defer d.lock.Unlock()
	compFn := func(e *ICECandidateExtended) bool {
		return isCandidateEqualTo(e.Local, c)
	}
	if slices.ContainsFunc[[]*ICECandidateExtended, *ICECandidateExtended](d.Local, compFn) {
		return
	}
	d.Local = append(d.Local, &ICECandidateExtended{
		Local:    c,
		Filtered: filtered,
	})
}

func (d *ICEConnectionDetails) AddRemoteCandidate(c webrtc.ICECandidateInit, filtered bool) {
	candidate, err := unmarshalICECandidate(c)
	if err != nil {
		d.logger.Errorw("could not unmarshal candidate", err, "candidate", c)
		return
	}

	d.lock.Lock()
	defer d.lock.Unlock()
	compFn := func(e *ICECandidateExtended) bool {
		return isICECandidateEqualTo(e.Remote, candidate)
	}
	if slices.ContainsFunc[[]*ICECandidateExtended, *ICECandidateExtended](d.Remote, compFn) {
		return
	}
	d.Remote = append(d.Remote, &ICECandidateExtended{
		Remote:   candidate,
		Filtered: filtered,
	})
}

func (d *ICEConnectionDetails) Clear() {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.Local = nil
	d.Remote = nil
	d.Type = ICEConnectionTypeUnknown
}

func (d *ICEConnectionDetails) SetSelectedPair(pair *webrtc.ICECandidatePair) {
	d.lock.Lock()
	defer d.lock.Unlock()
	remoteIdx := slices.IndexFunc[[]*ICECandidateExtended, *ICECandidateExtended](d.Remote, func(e *ICECandidateExtended) bool {
		return isICECandidateEqualToCandidate(e.Remote, pair.Remote)
	})
	if remoteIdx < 0 {
		// it's possible for prflx candidates to be generated by Pion, we'll add them
		candidate, err := unmarshalICECandidate(pair.Remote.ToJSON())
		if err != nil {
			d.logger.Errorw("could not unmarshal remote candidate", err, "candidate", pair.Remote)
			return
		}
		d.Remote = append(d.Remote, &ICECandidateExtended{
			Remote:   candidate,
			Filtered: false,
		})
		remoteIdx = len(d.Remote) - 1
	}
	remote := d.Remote[remoteIdx]
	remote.Selected = true

	localIdx := slices.IndexFunc[[]*ICECandidateExtended, *ICECandidateExtended](d.Local, func(e *ICECandidateExtended) bool {
		return isCandidateEqualTo(e.Local, pair.Local)
	})
	if localIdx < 0 {
		d.logger.Errorw("could not match local candidate", nil, "local", pair.Local)
		// should not happen
		return
	}
	local := d.Local[localIdx]
	local.Selected = true

	d.Type = ICEConnectionTypeUDP
	if pair.Remote.Protocol == webrtc.ICEProtocolTCP {
		d.Type = ICEConnectionTypeTCP
	}
	if pair.Remote.Typ == webrtc.ICECandidateTypeRelay {
		d.Type = ICEConnectionTypeTURN
	} else if pair.Remote.Typ == webrtc.ICECandidateTypePrflx {
		// if the remote relay candidate pings us *before* we get a relay candidate,
		// Pion would have created a prflx candidate with the same address as the relay candidate.
		// to report an accurate connection type, we'll compare to see if existing relay candidates match
		for _, other := range d.Remote {
			or := other.Remote
			if or.Type() == ice.CandidateTypeRelay &&
				pair.Remote.Address == or.Address() &&
				pair.Remote.Port == uint16(or.Port()) &&
				pair.Remote.Protocol.String() == or.NetworkType().NetworkShort() {
				d.Type = ICEConnectionTypeTURN
			}
		}
	}
}

func isCandidateEqualTo(c1, c2 *webrtc.ICECandidate) bool {
	if c1 == nil && c2 == nil {
		return true
	}
	if (c1 == nil && c2 != nil) || (c1 != nil && c2 == nil) {
		return false
	}
	return c1.Typ == c2.Typ &&
		c1.Protocol == c2.Protocol &&
		c1.Address == c2.Address &&
		c1.Port == c2.Port &&
		c1.Component == c2.Component &&
		c1.Foundation == c2.Foundation &&
		c1.Priority == c2.Priority &&
		c1.RelatedAddress == c2.RelatedAddress &&
		c1.RelatedPort == c2.RelatedPort &&
		c1.TCPType == c2.TCPType
}

func isICECandidateEqualTo(c1, c2 ice.Candidate) bool {
	if c1 == nil && c2 == nil {
		return true
	}
	if (c1 == nil && c2 != nil) || (c1 != nil && c2 == nil) {
		return false
	}
	return c1.Type() == c2.Type() &&
		c1.NetworkType() == c2.NetworkType() &&
		c1.Address() == c2.Address() &&
		c1.Port() == c2.Port() &&
		c1.Component() == c2.Component() &&
		c1.Foundation() == c2.Foundation() &&
		c1.Priority() == c2.Priority() &&
		c1.RelatedAddress().Equal(c2.RelatedAddress()) &&
		c1.TCPType() == c2.TCPType()
}

func isICECandidateEqualToCandidate(c1 ice.Candidate, c2 *webrtc.ICECandidate) bool {
	if c1 == nil && c2 == nil {
		return true
	}
	if (c1 == nil && c2 != nil) || (c1 != nil && c2 == nil) {
		return false
	}
	return c1.Type().String() == c2.Typ.String() &&
		c1.NetworkType().NetworkShort() == c2.Protocol.String() &&
		c1.Address() == c2.Address &&
		c1.Port() == int(c2.Port) &&
		c1.Component() == c2.Component &&
		c1.Foundation() == c2.Foundation &&
		c1.Priority() == c2.Priority &&
		c1.TCPType().String() == c2.TCPType
}

func unmarshalICECandidate(c webrtc.ICECandidateInit) (ice.Candidate, error) {
	candidateValue := strings.TrimPrefix(c.Candidate, "candidate:")
	return ice.UnmarshalCandidate(candidateValue)
}
