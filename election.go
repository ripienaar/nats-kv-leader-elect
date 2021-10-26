package election

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// State indicates the current state of the election
type State uint

type Election interface {
	// Start starts the election, interrupted by context. Blocks until stopped.
	Start(ctx context.Context) error
	// Stop stops the election
	Stop()
	// IsLeader determines if we are currently the leader
	IsLeader() bool
	// State is the current state
	State() State
}

const (
	// UnknownState indicates the state is unknown, like when the election is not started
	UnknownState State = 0
	// CandidateState is a campaigner that is not the leader
	CandidateState State = 1
	// LeaderState is the leader
	LeaderState State = 2
)

// Backoff controls the interval of campaigns
type Backoff interface {
	// Duration returns the time to sleep for the nth invocation
	Duration(n int) time.Duration
}

type election struct {
	opts  *options
	state State

	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	lastSeq uint64
	tries   int

	mu sync.Mutex
}

var skipValidate bool

func NewElection(name string, key string, bucket nats.KeyValue, opts ...Option) (Election, error) {
	e := &election{
		state:   UnknownState,
		lastSeq: math.MaxUint64,
		opts: &options{
			name:   name,
			key:    key,
			bucket: bucket,
		},
	}

	status, err := bucket.Status()
	if err != nil {
		return nil, err
	}

	e.opts.ttl = status.TTL()
	if !skipValidate {
		if e.opts.ttl < 30*time.Second {
			return nil, fmt.Errorf("bucket TTL should be 30 seconds or more")
		}
		if e.opts.ttl > time.Hour {
			return nil, fmt.Errorf("bucket TTL should be less than or equal to 1 hour")
		}
	}

	e.opts.cInterval = time.Duration(e.opts.ttl.Seconds()*0.75) * time.Second

	for _, opt := range opts {
		opt(e.opts)
	}

	if !skipValidate {
		if e.opts.cInterval.Seconds() < 5 {
			return nil, fmt.Errorf("campaign interval %v too small", e.opts.cInterval)
		}
		if e.opts.ttl.Seconds()-e.opts.cInterval.Seconds() < 5 {
			return nil, fmt.Errorf("campaign interval %v is too close to bucket ttl %v", e.opts.cInterval, e.opts.ttl)
		}
	}

	return e, nil
}

func (e *election) debugf(format string, a ...interface{}) {
	if e.opts.debug == nil {
		return
	}
	e.opts.debug(format, a...)
}

func (e *election) try() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.opts.campaignCb != nil {
		e.opts.campaignCb(e.state)
	}

	switch e.state {
	case LeaderState:
		seq, err := e.opts.bucket.Update(e.opts.key, []byte(e.opts.name), e.lastSeq)
		if err != nil {
			e.debugf("key update failed, moving to candidate state: %v\n", err)
			e.state = CandidateState
			e.lastSeq = math.MaxUint64
			if e.opts.lostCb != nil {
				e.opts.lostCb()
			}

			return err
		}
		e.lastSeq = seq

		return nil

	case CandidateState:
		seq, err := e.opts.bucket.Create(e.opts.key, []byte(e.opts.name))
		if err != nil {
			e.tries++
			return nil
		}

		e.lastSeq = seq
		e.state = LeaderState
		e.tries = 0

		// we notify the caller after interval so that other the past leader
		// has a chance to detect the leadership loss, else if the key got just
		// deleted right before the previous leader did a campaign he will think
		// he is leader for one more round of cInterval
		go func() {
			ctxSleep(e.ctx, e.opts.cInterval)
			if e.opts.wonCb != nil {
				e.opts.wonCb()
			}
		}()

		return nil

	default:
		return fmt.Errorf("campaigned while in unknown state")
	}
}

func (e *election) campaign(wg *sync.WaitGroup) error {
	defer wg.Done()

	e.mu.Lock()
	e.state = CandidateState
	e.mu.Unlock()

	// spread out startups a bit
	time.Sleep(time.Duration(rand.Intn(int(e.opts.cInterval.Milliseconds()))))

	var ticker *time.Ticker
	if e.opts.bo != nil {
		ticker = time.NewTicker(e.opts.bo.Duration(0))
	} else {
		ticker = time.NewTicker(e.opts.cInterval)
	}

	for {
		select {
		case <-ticker.C:
			err := e.try()
			if err != nil {
				e.debugf("election attempt failed: %v", err)
			}

			if e.opts.bo != nil {
				ticker.Reset(e.opts.bo.Duration(e.tries))
			}
		case <-e.ctx.Done():
			ticker.Stop()
			e.stop()

			if e.opts.lostCb != nil && e.IsLeader() {
				e.opts.lostCb()
			}

			return nil
		}
	}
}

func (e *election) stop() {
	e.mu.Lock()
	e.started = false
	e.cancel()
	e.state = UnknownState
	e.lastSeq = math.MaxUint64
	e.mu.Unlock()
}

func (e *election) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return fmt.Errorf("already running")
	}

	e.ctx, e.cancel = context.WithCancel(ctx)
	e.started = true
	e.mu.Unlock()

	wg := &sync.WaitGroup{}
	wg.Add(1)

	err := e.campaign(wg)
	if err != nil {
		e.stop()
		return err
	}

	wg.Wait()

	return nil
}

func (e *election) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.started {
		return
	}

	if e.cancel != nil {
		e.cancel()
	}

	e.stop()
}

func (e *election) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.state == LeaderState
}

func (e *election) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.state
}

func ctxSleep(ctx context.Context, duration time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	sctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	<-sctx.Done()

	return ctx.Err()
}
