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

package pacer

import (
	"sync"
	"time"

	"github.com/livekit/livekit-server/pkg/sfu/ccutils"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

type Packet struct {
	Header             *rtp.Header
	HeaderSize         int
	Payload            []byte
	IsRTX              bool
	ProbeClusterId     ccutils.ProbeClusterId
	IsProbe            bool
	AbsSendTimeExtID   uint8
	TransportWideExtID uint8
	WriteStream        webrtc.TrackLocalWriter
	Pool               *sync.Pool
	PoolEntity         *[]byte
}

type Pacer interface {
	Enqueue(p *Packet)
	Stop()

	SetInterval(interval time.Duration)
	SetBitrate(bitrate int)

	SetPacerProbeObserverListener(listener PacerProbeObserverListener)
	StartProbeCluster(probeClusterId ccutils.ProbeClusterId, desiredBytes int)
	EndProbeCluster(probeClusterId ccutils.ProbeClusterId)
	AbortProbeCluster(probeClusterId ccutils.ProbeClusterId)
}

type PacerProbeObserverClusterInfo struct {
	ProbeClusterId       ccutils.ProbeClusterId
	DesiredBytes         int
	StartTime            int64
	EndTime              int64
	BytesProbe           int
	BytesNonProbePrimary int
	BytesNonProbeRTX     int
}

type PacerProbeObserverListener interface {
	OnPacerProbeObserverClusterComplete(info PacerProbeObserverClusterInfo)
}

// ------------------------------------------------
