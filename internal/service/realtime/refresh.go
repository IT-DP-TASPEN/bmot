package realtime

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"
)

type Fetcher interface {
	Fetch(context.Context, time.Time) (Dataset, error)
}

type GenerationStore interface {
	Start(context.Context, time.Time) (int64, error)
	Publish(context.Context, int64, Dataset) error
	Fail(context.Context, int64, string) error
	Retain(context.Context, int) error
}

type Refresher struct {
	Store      GenerationStore
	Fetcher    Fetcher
	Retention  int
	StaleAfter time.Duration
	running    atomic.Bool
}

var ErrAlreadyRunning = errors.New("refresh already running")

func Today() time.Time {
	now := time.Now().In(time.FixedZone("WIB", 7*3600))
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func (r *Refresher) Running() bool { return r.running.Load() }

// Trigger starts the same refresh path used by the scheduler without waiting in the request.
func (r *Refresher) Trigger() bool {
	if !r.running.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer r.running.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := r.refresh(ctx); err != nil {
			log.Printf("realtime refresh failed: %v", err)
		}
	}()
	return true
}

func (r *Refresher) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer r.running.Store(false)
	return r.refresh(ctx)
}

func (r *Refresher) refresh(ctx context.Context) error {
	date := Today()
	id, err := r.Store.Start(ctx, date)
	if err != nil {
		return err
	}
	d, err := r.Fetcher.Fetch(ctx, date)
	if err == nil && !d.Date.Equal(date) {
		err = errors.New("Fincloud business date differs from refresh date")
	}
	if err == nil {
		err = r.Store.Publish(ctx, id, d)
	}
	if err != nil {
		failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if failErr := r.Store.Fail(failCtx, id, err.Error()); failErr != nil {
			log.Printf("recording failed snapshot %d: %v", id, failErr)
		}
		if retainErr := r.Store.Retain(failCtx, r.Retention); retainErr != nil {
			log.Printf("snapshot retention failed: %v", retainErr)
		}
		return err
	}
	if err := r.Store.Retain(ctx, r.Retention); err != nil {
		log.Printf("snapshot retention failed: %v", err)
	}
	return nil
}

func (r *Refresher) Schedule(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	r.Trigger()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Trigger()
		}
	}
}

func (r *Refresher) Stale(p *Published, now time.Time) bool {
	local := now.In(time.FixedZone("WIB", 7*3600))
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	if p == nil || !p.Date.Equal(today) {
		return true
	}
	return now.Sub(p.PublishedAt) > r.StaleAfter
}
