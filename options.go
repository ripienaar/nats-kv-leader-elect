package election

import (
	"time"

	"github.com/nats-io/nats.go"
)

// Option configures the election system
type Option func(o *options)

type options struct {
	name       string
	key        string
	bucket     nats.KeyValue
	ttl        time.Duration
	cInterval  time.Duration
	wonCb      func()
	lostCb     func()
	campaignCb func(s State)
	bo         Backoff
	noSplay    bool
	debug      func(format string, a ...interface{})
}

// WithBackoff will use the provided Backoff timer source to decrease campaign intervals over time
func WithBackoff(bo Backoff) Option {
	return func(o *options) { o.bo = bo }
}

// WithoutSplay disables random sleeping before election starts
func WithoutSplay() Option {
	return func(o *options) { o.noSplay = true }
}

// OnWon is a callback called when winning an election
func OnWon(cb func()) Option {
	return func(o *options) { o.wonCb = cb }
}

// OnLost is a callback called when losing an election
func OnLost(cb func()) Option {
	return func(o *options) { o.lostCb = cb }
}

// OnCampaign is called each time a campaign is done by the leader or a candidate
func OnCampaign(cb func(s State)) Option {
	return func(o *options) { o.campaignCb = cb }
}

// WithDebug sets a function to do debug logging with
func WithDebug(cb func(format string, a ...interface{})) Option {
	return func(o *options) { o.debug = cb }
}
