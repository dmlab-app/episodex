package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Task represents a scheduled task
type Task struct {
	Name     string
	Schedule Schedule
	Handler  func(context.Context) error
}

// Schedule defines when a task should run
type Schedule interface {
	NextRun(lastRun time.Time) time.Time
}

// IntervalSchedule runs task at fixed intervals
type IntervalSchedule struct {
	Interval time.Duration
}

// NextRun calculates next run time for interval schedule
func (s *IntervalSchedule) NextRun(lastRun time.Time) time.Time {
	if lastRun.IsZero() {
		return time.Now()
	}
	return lastRun.Add(s.Interval)
}

// DailySchedule runs task once a day at specific hour
type DailySchedule struct {
	Hour int // 0-23
}

// NextRun calculates next run time for daily schedule
func (s *DailySchedule) NextRun(lastRun time.Time) time.Time {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, now.Location())

	// If we already passed today's scheduled time, schedule for tomorrow
	if now.After(next) {
		next = next.Add(24 * time.Hour)
	}

	// If we already ran today, schedule for tomorrow
	if !lastRun.IsZero() && lastRun.Day() == now.Day() && lastRun.Month() == now.Month() && lastRun.Year() == now.Year() {
		next = next.Add(24 * time.Hour)
	}

	return next
}

// Scheduler manages periodic tasks
type Scheduler struct {
	tasks    []Task
	ctx      context.Context
	cancel   context.CancelFunc
	ticker   *time.Ticker
	lastRuns map[string]time.Time
}

// New creates a new scheduler
func New() *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		tasks:    make([]Task, 0),
		ctx:      ctx,
		cancel:   cancel,
		ticker:   time.NewTicker(1 * time.Minute),
		lastRuns: make(map[string]time.Time),
	}
}

// AddTask adds a task to the scheduler
func (s *Scheduler) AddTask(task Task) {
	s.tasks = append(s.tasks, task)
	slog.Info("Scheduled task added", "name", task.Name)
}

// Start starts the scheduler
func (s *Scheduler) Start() {
	slog.Info("Scheduler started")

	// Run interval tasks immediately on startup
	for _, task := range s.tasks {
		if _, ok := task.Schedule.(*IntervalSchedule); ok {
			slog.Info("Running task on startup", "name", task.Name)
			go s.runTask(task)
			s.lastRuns[task.Name] = time.Now()
		}
	}

	for {
		select {
		case <-s.ctx.Done():
			slog.Info("Scheduler stopped")
			return

		case now := <-s.ticker.C:
			for _, task := range s.tasks {
				lastRun := s.lastRuns[task.Name]
				nextRun := task.Schedule.NextRun(lastRun)

				if now.After(nextRun) || now.Equal(nextRun) {
					go s.runTask(task)
					s.lastRuns[task.Name] = now
				}
			}
		}
	}
}

// runTask executes a single task
func (s *Scheduler) runTask(task Task) {
	slog.Info("Running scheduled task", "name", task.Name)
	start := time.Now()

	if err := task.Handler(s.ctx); err != nil {
		slog.Error("Task failed", "name", task.Name, "error", err, "duration", time.Since(start))
		return
	}

	slog.Info("Task completed", "name", task.Name, "duration", time.Since(start))
}

// Stop stops the scheduler gracefully
func (s *Scheduler) Stop() {
	slog.Info("Stopping scheduler...")
	s.ticker.Stop()
	s.cancel()
}

// StartAsync starts the scheduler in a goroutine
func (s *Scheduler) StartAsync() {
	go s.Start()
}
