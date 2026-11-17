package main

import (
	"bytes"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const deskSampleRate int64 = 16000
const deskClockPrecision = 100 * time.Millisecond

type deskClockAnchor struct {
	sample int64
	at     time.Time
}

// Capture timestamps and PCM counts travel through separate FFmpeg pipes.
// Keep sparse discontinuity anchors, not a first-read wall clock: dropped
// capture time must not disappear after sleep or sustained device drops.
type deskCaptureClock struct {
	mu      sync.Mutex
	anchors []deskClockAnchor
	samples int64
	started time.Time
	ready   chan struct{}
	err     error
}

func newDeskCaptureClock(started time.Time) *deskCaptureClock {
	return &deskCaptureClock{started: started, ready: make(chan struct{})}
}
func sampleDuration(samples int64) time.Duration {
	return time.Duration(samples/deskSampleRate)*time.Second + time.Duration(samples%deskSampleRate)*time.Second/time.Duration(deskSampleRate)
}
func (c *deskCaptureClock) signalLocked() { close(c.ready); c.ready = make(chan struct{}) }
func (c *deskCaptureClock) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		c.signalLocked()
	}
}
func (c *deskCaptureClock) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
func (c *deskCaptureClock) addFrame(pts, rate, samples int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	if rate != deskSampleRate || samples <= 0 {
		c.err = errors.New("录音组件返回了无效的采样时间")
		c.signalLocked()
		return
	}
	at := time.Unix(pts/rate, (pts%rate)*int64(time.Second)/rate)
	// -copyts and wall-clock input timestamps are required. A relative timestamp
	// must fail instead of quietly producing a date in 1970.
	if len(c.anchors) == 0 && at.Before(c.started.Add(-time.Minute)) {
		c.err = errors.New("录音组件未提供实际录音时间")
		c.signalLocked()
		return
	}
	appendAnchor := len(c.anchors) == 0
	if !appendAnchor {
		last := c.anchors[len(c.anchors)-1]
		expected := last.at.Add(sampleDuration(c.samples - last.sample))
		delta := at.Sub(expected)
		appendAnchor = delta > deskClockPrecision || delta < -deskClockPrecision
	}
	if appendAnchor {
		c.anchors = append(c.anchors, deskClockAnchor{sample: c.samples, at: at})
	}
	c.samples += samples
	c.signalLocked()
}
func (c *deskCaptureClock) waitSamples(samples int64) error {
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		c.mu.Lock()
		if c.err != nil {
			err := c.err
			c.mu.Unlock()
			return err
		}
		if c.samples >= samples {
			c.mu.Unlock()
			return nil
		}
		ready := c.ready
		c.mu.Unlock()
		select {
		case <-ready:
		case <-timeout.C:
			err := errors.New("录音采样时间信息缺失，请重新开始守候；已保存的片段仍保留")
			c.fail(err)
			return err
		}
	}
}
func (c *deskCaptureClock) timeAt(ms int64) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeAtLocked(ms)
}
func (c *deskCaptureClock) timeAtLocked(ms int64) (time.Time, bool) {
	if len(c.anchors) == 0 {
		return time.Time{}, false
	}
	sample := ms * deskSampleRate / 1000
	index := sort.Search(len(c.anchors), func(i int) bool { return c.anchors[i].sample > sample }) - 1
	if index < 0 {
		return time.Time{}, false
	}
	anchor := c.anchors[index]
	return anchor.at.Add(sampleDuration(sample - anchor.sample)), true
}
func (c *deskCaptureClock) clipAnchors(startMS, endMS int64) []DeskTimeAnchor {
	c.mu.Lock()
	defer c.mu.Unlock()
	first, ok := c.timeAtLocked(startMS)
	if !ok {
		return nil
	}
	result := []DeskTimeAnchor{{OffsetMS: 0, RecordedAt: first.Format(time.RFC3339Nano)}}
	for _, anchor := range c.anchors {
		ms := anchor.sample * 1000 / deskSampleRate
		if ms > startMS && ms < endMS {
			result = append(result, DeskTimeAnchor{OffsetMS: ms - startMS, RecordedAt: anchor.at.Format(time.RFC3339Nano)})
		}
	}
	return result
}

var deskFrameTiming = regexp.MustCompile(`\bpts:(-?[0-9]+)\s.*\brate:([0-9]+)\s+nb_samples:([0-9]+)\b`)

// FFmpeg's ashowinfo produces metadata before writing each output frame. Normal
// stderr (which may contain local paths) is discarded; only exact PTS integers
// and sample counts enter the clock. pts_time is rounded and is not used.
type deskTimestampWriter struct {
	clock   *deskCaptureClock
	pending []byte
}

func (w *deskTimestampWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := string(w.pending[:i])
		w.pending = w.pending[i+1:]
		if !strings.Contains(line, "ashowinfo") || !strings.Contains(line, "nb_samples:") {
			continue
		}
		match := deskFrameTiming.FindStringSubmatch(line)
		if match == nil {
			w.clock.fail(errors.New("录音组件返回了无效的采样时间"))
			continue
		}
		values := make([]int64, 3)
		valid := true
		for i := range values {
			n, err := strconv.ParseInt(match[i+1], 10, 64)
			if err != nil {
				valid = false
			}
			values[i] = n
		}
		if valid {
			w.clock.addFrame(values[0], values[1], values[2])
		} else {
			w.clock.fail(errors.New("录音组件返回了无效的采样时间"))
		}
	}
	// Operational logs are intentionally not retained. Avoid unbounded partial
	// lines if a component reports malformed output.
	if len(w.pending) > 64*1024 {
		w.pending = nil
		w.clock.fail(errors.New("录音时间信息格式无效"))
	}
	return len(p), nil
}
