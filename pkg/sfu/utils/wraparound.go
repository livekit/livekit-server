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

package utils

import (
	"unsafe"
)

type number interface {
	uint16 | uint32
}

type extendedNumber interface {
	uint32 | uint64
}

type WrapAround[T number, ET extendedNumber] struct {
	fullRange ET

	initialized bool
	start       T
	highest     T
	cycles      ET
}

func NewWrapAround[T number, ET extendedNumber]() *WrapAround[T, ET] {
	var t T
	return &WrapAround[T, ET]{
		fullRange: 1 << (unsafe.Sizeof(t) * 8),
	}
}

func (w *WrapAround[T, ET]) Seed(from *WrapAround[T, ET]) {
	w.initialized = from.initialized
	w.start = from.start
	w.highest = from.highest
	w.cycles = from.cycles
}

type WrapAroundUpdateResult[ET extendedNumber] struct {
	IsRestart          bool
	PreExtendedStart   ET // valid only if IsRestart = true
	PreExtendedHighest ET
	ExtendedVal        ET
}

func (w *WrapAround[T, ET]) Update(val T) (result WrapAroundUpdateResult[ET]) {
	if !w.initialized {
		result.PreExtendedHighest = ET(val) - 1
		result.ExtendedVal = ET(val)

		w.start = val
		w.highest = val
		w.initialized = true
		return
	}

	result.PreExtendedHighest = w.GetExtendedHighest()

	gap := val - w.highest
	if gap == 0 || gap > T(w.fullRange>>1) {
		// duplicate OR out-of-order
		result.IsRestart, result.PreExtendedStart, result.ExtendedVal = w.maybeAdjustStart(val)
		return
	}

	// in-order
	if val < w.highest {
		w.cycles += w.fullRange
	}
	w.highest = val

	result.ExtendedVal = w.getExtendedHighest(w.cycles, val)
	return
}

func (w *WrapAround[T, ET]) RollbackRestart(ev ET) {
	if w.isWrapBack(w.start, T(ev)) {
		w.cycles -= w.fullRange
	}
	w.start = T(ev)
}

func (w *WrapAround[T, ET]) ResetHighest(ev ET) {
	w.highest = T(ev)
	w.cycles = ev & ^(w.fullRange - 1)
}

func (w *WrapAround[T, ET]) GetStart() T {
	return w.start
}

func (w *WrapAround[T, ET]) GetExtendedStart() ET {
	return ET(w.start)
}

func (w *WrapAround[T, ET]) GetHighest() T {
	return w.highest
}

func (w *WrapAround[T, ET]) GetExtendedHighest() ET {
	return w.getExtendedHighest(w.cycles, w.highest)
}

func (w *WrapAround[T, ET]) maybeAdjustStart(val T) (isRestart bool, preExtendedStart ET, extendedVal ET) {
	// re-adjust start if necessary. The conditions are
	// 1. Not seen more than half the range yet
	// 1. wrap back compared to start and not completed a half cycle, sequences like (10, 65530) in uint16 space
	// 2. no wrap around, but out-of-order compared to start and not completed a half cycle , sequences like (10, 9), (65530, 65528) in uint16 space
	cycles := w.cycles
	totalNum := w.GetExtendedHighest() - w.GetExtendedStart() + 1
	if totalNum > (w.fullRange >> 1) {
		if w.isWrapBack(val, w.highest) {
			cycles -= w.fullRange
		}
		extendedVal = w.getExtendedHighest(cycles, val)
		return
	}

	if val-w.start > T(w.fullRange>>1) {
		// out-of-order with existing start => a new start
		isRestart = true
		preExtendedStart = w.GetExtendedStart()

		if w.isWrapBack(val, w.highest) {
			w.cycles = w.fullRange
			cycles = 0
		}
		w.start = val
	} else {
		if w.isWrapBack(val, w.highest) {
			cycles -= w.fullRange
		}
	}
	extendedVal = w.getExtendedHighest(cycles, val)
	return
}

func (w *WrapAround[T, ET]) isWrapBack(earlier T, later T) bool {
	return ET(later) < (w.fullRange>>1) && ET(earlier) >= (w.fullRange>>1)
}

func (w *WrapAround[T, ET]) getExtendedHighest(cycles ET, val T) ET {
	return cycles + ET(val)
}