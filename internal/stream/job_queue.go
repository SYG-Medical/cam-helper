package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nystavision/internal/logging"
)

const (
	JobStatusPending    = "pending"
	JobStatusProcessing = "processing"
	JobStatusCompleted  = "completed"
	JobStatusFailed     = "failed"
)

// ProcessingJob represents a background video processing task.
type ProcessingJob struct {
	ID            string          `json:"id"`
	PatientDir    string          `json:"patient_dir"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	CompletedAt   time.Time       `json:"completed_at,omitempty"`
	Error         string          `json:"error,omitempty"`
	SessionData   json.RawMessage `json:"session_data"`
	// Composite mode fields
	IsComposite   bool   `json:"is_composite,omitempty"`
	CompositeFile string `json:"composite_file,omitempty"`
	Verified      bool   `json:"verified,omitempty"`
}

// JobQueue manages background video post-processing tasks.
type JobQueue struct {
	mu         sync.Mutex
	jobsDir    string
	postProc   *PostProcessor
	logger     *logging.Logger
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	queue      chan ProcessingJob
	
	onProgress func(jobID string, progress float64, status string, isComposite bool)
	onComplete func(jobID string, status string, isComposite bool)

	activeJobs int
	totalJobs  int
}

// NewJobQueue initializes the persistent job queue.
func NewJobQueue(recordingsDir string, postProc *PostProcessor, logger *logging.Logger) (*JobQueue, error) {
	jobsDir := filepath.Join(recordingsDir, ".jobs")
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create jobs dir: %w", err)
	}

	return &JobQueue{
		jobsDir:  jobsDir,
		postProc: postProc,
		logger:   logger,
		queue:    make(chan ProcessingJob, 100),
	}, nil
}

// GetProgressInfo returns the number of active and total pending/processing jobs.
func (jq *JobQueue) GetProgressInfo() (active int, total int) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return jq.activeJobs, jq.totalJobs
}

// SetCallbacks sets UI callbacks.
func (jq *JobQueue) SetCallbacks(onProgress func(id string, p float64, s string, isComposite bool), onComplete func(id string, s string, isComposite bool)) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	jq.onProgress = onProgress
	jq.onComplete = onComplete
}

// Start launches the background worker.
func (jq *JobQueue) Start(ctx context.Context) {
	jq.mu.Lock()
	if jq.cancel != nil {
		jq.mu.Unlock()
		return // Already running
	}
	ctx, cancel := context.WithCancel(ctx)
	jq.cancel = cancel
	jq.wg.Add(1)
	jq.mu.Unlock()

	go jq.worker(ctx)
}

// Stop gracefully shuts down the worker.
func (jq *JobQueue) Stop() {
	jq.mu.Lock()
	if jq.cancel != nil {
		jq.cancel()
		jq.cancel = nil
	}
	jq.mu.Unlock()
	jq.wg.Wait()
}

// Enqueue adds a new standard (per-camera) processing job.
func (jq *JobQueue) Enqueue(snap RecordingSessionSnapshot, patientDir string) error {
	snapData, err := MarshalSnapshot(snap)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	job := ProcessingJob{
		ID:          snap.ID,
		PatientDir:  patientDir,
		Status:      JobStatusPending,
		CreatedAt:   time.Now(),
		SessionData: snapData,
		IsComposite: false,
	}

	if err := jq.saveJob(job); err != nil {
		return fmt.Errorf("save job: %w", err)
	}

	jq.mu.Lock()
	jq.totalJobs++
	jq.mu.Unlock()

	jq.queue <- job
	return nil
}

// EnqueueComposite adds a new composite decompose job. The job will crop each
// camera cell out of compositeFile and produce individual camera videos.
func (jq *JobQueue) EnqueueComposite(snap RecordingSessionSnapshot, patientDir, compositeFile string) error {
	snapData, err := MarshalSnapshot(snap)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	job := ProcessingJob{
		ID:            snap.ID,
		PatientDir:    patientDir,
		Status:        JobStatusPending,
		CreatedAt:     time.Now(),
		SessionData:   snapData,
		IsComposite:   true,
		CompositeFile: compositeFile,
	}

	if err := jq.saveJob(job); err != nil {
		return fmt.Errorf("save job: %w", err)
	}

	jq.mu.Lock()
	jq.totalJobs++
	jq.mu.Unlock()

	jq.queue <- job
	return nil
}

// Resume loads pending/processing jobs from disk and queues them.
func (jq *JobQueue) Resume() error {
	entries, err := os.ReadDir(jq.jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read jobs dir: %w", err)
	}

	var jobs []ProcessingJob
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(jq.jobsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			jq.logger.Printf("Failed to read job file %s: %v", path, err)
			continue
		}
		var job ProcessingJob
		if err := json.Unmarshal(data, &job); err != nil {
			jq.logger.Printf("Failed to parse job file %s: %v", path, err)
			continue
		}
		if job.Status == JobStatusPending || job.Status == JobStatusProcessing {
			// Reset status to pending
			job.Status = JobStatusPending
			jq.saveJob(job)
			jobs = append(jobs, job)
		}
	}

	jq.mu.Lock()
	for _, job := range jobs {
		jq.totalJobs++
		jq.queue <- job
	}
	jq.mu.Unlock()
	return nil
}

// GetCompletedJobs returns all completed jobs (used for cleanup).
func (jq *JobQueue) GetCompletedJobs() ([]ProcessingJob, error) {
	entries, err := os.ReadDir(jq.jobsDir)
	if err != nil {
		return nil, fmt.Errorf("read jobs dir: %w", err)
	}

	var jobs []ProcessingJob
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(jq.jobsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var job ProcessingJob
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}
		if job.Status == JobStatusCompleted {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

// DeleteJob removes a job file from disk.
func (jq *JobQueue) DeleteJob(jobID string) {
	path := filepath.Join(jq.jobsDir, jobID+".json")
	os.Remove(path)
}

func (jq *JobQueue) saveJob(job ProcessingJob) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(jq.jobsDir, job.ID+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (jq *JobQueue) worker(ctx context.Context) {
	defer jq.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jq.queue:
			jq.processJob(ctx, job)
		}
	}
}

func (jq *JobQueue) processJob(ctx context.Context, job ProcessingJob) {
	jq.mu.Lock()
	jq.activeJobs++
	jq.mu.Unlock()
	
	job.Status = JobStatusProcessing
	_ = jq.saveJob(job)

	if jq.logger != nil {
		jq.logger.Printf("[job_queue] Processing job %s for %s (composite=%v)", job.ID, job.PatientDir, job.IsComposite)
	}

	snap, err := UnmarshalSnapshot(job.SessionData)
	if err != nil {
		job.Status = JobStatusFailed
		job.Error = fmt.Sprintf("unmarshal snapshot: %v", err)
		_ = jq.saveJob(job)
		if jq.logger != nil {
			jq.logger.Printf("[job_queue] Failed to unmarshal session for job %s: %v", job.ID, err)
		}
		if jq.onComplete != nil {
			jq.onComplete(job.ID, job.Status, job.IsComposite)
		}
		return
	}

	var cameras []CameraRecording
	for _, cam := range snap.Cameras {
		cameras = append(cameras, *cam)
	}

	if job.IsComposite {
		// Composite decompose path: crop individual camera videos from the composite file.
		progressCh := make(chan float64, 10)
		go func() {
			var lastVal float64
			var lastTime time.Time
			for p := range progressCh {
				val := p * 100.0
				now := time.Now()
				if val-lastVal >= 1.0 || now.Sub(lastTime) >= 200*time.Millisecond || val >= 100.0 {
					jq.mu.Lock()
					cb := jq.onProgress
					jq.mu.Unlock()
					if cb != nil {
						cb(job.ID, val, JobStatusProcessing, job.IsComposite)
					}
					lastVal = val
					lastTime = now
				}
			}
		}()

		result := jq.postProc.DecomposeComposite(ctx, job.CompositeFile, cameras, snap.Cols, job.PatientDir, progressCh)
		close(progressCh)

		job.CompletedAt = time.Now()
		if result.Err != nil && result.Err != context.Canceled {
			job.Status = JobStatusFailed
			job.Error = result.Err.Error()
			if jq.logger != nil {
				jq.logger.Printf("[job_queue] Composite job %s failed: %v", job.ID, result.Err)
			}
		} else if result.Err == context.Canceled {
			job.Status = JobStatusPending
			_ = jq.saveJob(job)
			jq.mu.Lock()
			jq.activeJobs--
			jq.mu.Unlock()
			return
		} else {
			job.Status = JobStatusCompleted
			if jq.logger != nil {
				jq.logger.Printf("[job_queue] Composite job %s completed", job.ID)
			}
			info, infoErr := LoadPatientInfo(job.PatientDir)
			if infoErr == nil {
				var newVids []VideoFile
				for _, file := range result.Files {
					// Find matching camera to get role metadata
					var camRole, eyeSide, camName string
					for _, cam := range cameras {
						if strings.Contains(filepath.Base(file), sanitizeFilename(cam.ID)) {
							camRole = cam.CameraRole
							eyeSide = cam.EyeSide
							camName = cam.Name
							break
						}
					}
					newVids = append(newVids, VideoFile{
						FileName:   filepath.Base(file),
						Type:       "camera",
						Camera:     camName,
						CameraType: camRole,
						EyeSide:    eyeSide,
					})
				}
				// Append to last maneuver (preferred) or fall back to flat Videos
				if n := len(info.Maneuvers); n > 0 {
					info.Maneuvers[n-1].Videos = append(info.Maneuvers[n-1].Videos, newVids...)
				} else {
					info.Videos = append(info.Videos, newVids...)
				}
				_ = SavePatientInfo(job.PatientDir, info)

				// Verify outputs in background (non-blocking)
				go jq.verifyJob(job, info)
			}
		}
		_ = jq.saveJob(job)
		jq.mu.Lock()
		jq.activeJobs--
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed {
			jq.totalJobs--
		}
		cb := jq.onComplete
		jq.mu.Unlock()
		if cb != nil {
			cb(job.ID, job.Status, job.IsComposite)
		}
		return
	}

	// Standard (per-camera) path
	progressCh := make(chan float64, 10)
	go func() {
		var lastVal float64
		var lastTime time.Time
		for p := range progressCh {
			val := p * 70.0
			now := time.Now()
			if val-lastVal >= 1.0 || now.Sub(lastTime) >= 200*time.Millisecond || val >= 70.0 {
				jq.mu.Lock()
				cb := jq.onProgress
				jq.mu.Unlock()
				if cb != nil {
					cb(job.ID, val, JobStatusProcessing, job.IsComposite)
				}
				lastVal = val
				lastTime = now
			}
		}
	}()

	result := jq.postProc.ProcessIndividualCameras(ctx, snap, cameras, job.PatientDir, progressCh)
	close(progressCh)

	var resultGen ProcessResult
	if result.Err == nil || result.Err == context.Canceled {
		progressChGen := make(chan float64, 10)
		go func() {
			var lastVal float64
			var lastTime time.Time
			for p := range progressChGen {
				val := 70.0 + p*30.0
				now := time.Now()
				if val-lastVal >= 1.0 || now.Sub(lastTime) >= 200*time.Millisecond || val >= 100.0 {
					jq.mu.Lock()
					cb := jq.onProgress
					jq.mu.Unlock()
					if cb != nil {
						cb(job.ID, val, JobStatusProcessing, job.IsComposite)
					}
					lastVal = val
					lastTime = now
				}
			}
		}()
		resultGen = jq.postProc.ProcessGeneralOnly(ctx, snap, cameras, job.PatientDir, progressChGen, false)
		close(progressChGen)
	}

	job.CompletedAt = time.Now()

	if result.Err != nil && result.Err != context.Canceled {
		job.Status = JobStatusFailed
		job.Error = result.Err.Error()
		if jq.logger != nil {
			jq.logger.Printf("[job_queue] Job %s failed: %v", job.ID, result.Err)
		}
	} else if resultGen.Err != nil && resultGen.Err != context.Canceled {
		job.Status = JobStatusFailed
		job.Error = resultGen.Err.Error()
		if jq.logger != nil {
			jq.logger.Printf("[job_queue] Job %s failed at general video: %v", job.ID, resultGen.Err)
		}
	} else if result.Err == context.Canceled || resultGen.Err == context.Canceled {
		// If cancelled, push back to queue or leave as pending
		job.Status = JobStatusPending
		_ = jq.saveJob(job)
		return
	} else {
		job.Status = JobStatusCompleted
		if jq.logger != nil {
			jq.logger.Printf("[job_queue] Job %s completed successfully", job.ID)
		}

		// Update patient info: camera videos + HQ general video go into the last maneuver
		info, err := LoadPatientInfo(job.PatientDir)
		if err == nil {
			var newVids []VideoFile
			for _, file := range result.Files {
				// Find matching camera to get role metadata
				var camRole, eyeSide, camName string
				for _, cam := range cameras {
					if strings.Contains(filepath.Base(file), sanitizeFilename(cam.ID)) {
						camRole = cam.CameraRole
						eyeSide = cam.EyeSide
						camName = cam.Name
						break
					}
				}
				newVids = append(newVids, VideoFile{
					FileName:   filepath.Base(file),
					Type:       "camera",
					Camera:     camName,
					CameraType: camRole,
					EyeSide:    eyeSide,
				})
			}
			for _, file := range resultGen.Files {
				newVids = append(newVids, VideoFile{
					FileName: filepath.Base(file),
					Type:     "general",
				})
			}
			// Append to last maneuver (preferred) or fall back to flat Videos
			if n := len(info.Maneuvers); n > 0 {
				info.Maneuvers[n-1].Videos = append(info.Maneuvers[n-1].Videos, newVids...)
			} else {
				info.Videos = append(info.Videos, newVids...)
			}
			_ = SavePatientInfo(job.PatientDir, info)

			// Verify outputs in background (non-blocking)
			go jq.verifyJob(job, info)
		}
	}

	_ = jq.saveJob(job)

	jq.mu.Lock()
	jq.activeJobs--
	if job.Status == JobStatusCompleted || job.Status == JobStatusFailed {
		jq.totalJobs--
	}
	cb := jq.onComplete
	jq.mu.Unlock()
	if cb != nil {
		cb(job.ID, job.Status, job.IsComposite)
	}
}

// verifyJob runs in the background to verify ffmpeg output integrity
// and marks the job as verified.
func (jq *JobQueue) verifyJob(job ProcessingJob, info PatientInfo) {
	allValid := true

	// Gather videos from the last maneuver, fallback to flat Videos
	var videos []VideoFile
	if len(info.Maneuvers) > 0 {
		videos = info.Maneuvers[len(info.Maneuvers)-1].Videos
	} else {
		videos = info.Videos
	}

	for _, vid := range videos {
		vidPath := filepath.Join(job.PatientDir, vid.FileName)
		if err := jq.postProc.VerifyOutput(vidPath); err != nil {
			allValid = false
			break
		}
	}

	if allValid {
		job.Verified = true
		job.Status = JobStatusCompleted
		_ = jq.saveJob(job)
		if jq.logger != nil {
			jq.logger.Printf("[job_queue] Successfully verified job %s outputs", job.ID)
		}
	}
}

