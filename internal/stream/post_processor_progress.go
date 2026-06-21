package stream

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type progressTracker struct {
	mu          sync.Mutex
	totalJobs   int
	completed   int
	activeJobs  map[string]float64
	progressOut chan<- float64
}

func (pt *progressTracker) updateJob(id string, fraction float64) {
	pt.mu.Lock()
	pt.activeJobs[id] = fraction

	sum := float64(pt.completed)
	for _, f := range pt.activeJobs {
		sum += f
	}
	val := sum / float64(pt.totalJobs)
	pt.mu.Unlock()

	if pt.progressOut != nil {
		select {
		case pt.progressOut <- val:
		default:
		}
	}
}

func (pt *progressTracker) finishJob(id string) {
	pt.mu.Lock()
	delete(pt.activeJobs, id)
	pt.completed++
	sum := float64(pt.completed)
	for _, f := range pt.activeJobs {
		sum += f
	}
	val := sum / float64(pt.totalJobs)
	pt.mu.Unlock()

	if pt.progressOut != nil {
		select {
		case pt.progressOut <- val:
		default:
		}
	}
}

func parseFFmpegTime(t string) (time.Duration, error) {
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	s, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("invalid time numbers")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s*float64(time.Second)), nil
}

func ffmpegSplitFunc(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[0:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
