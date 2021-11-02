package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/nats.go"
	election "github.com/ripienaar/nats-kv-leader-elect"
)

type worker struct {
	name   string
	leader bool
	mu     sync.Mutex
}

func (w *worker) win() {
	w.mu.Lock()
	w.leader = true
	log.Printf("%s: bacame leader", w.name)
	w.mu.Unlock()
}

func (w *worker) lost() {
	w.mu.Lock()
	w.leader = false
	log.Printf("%s: lost leadership", w.name)
	w.mu.Unlock()
}

func (w *worker) doIfLeader(cb func()) {
	w.mu.Lock()
	leader := w.leader
	w.mu.Unlock()

	if !leader {
		return
	}

	cb()
}

func (w *worker) run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(time.Second)

	for {
		select {
		case <-ticker.C:
			w.doIfLeader(func() {
				log.Printf("%s: doing work", w.name)
			})
		case <-ctx.Done():
			return
		}
	}
}

func startWorker(ctx context.Context, wg *sync.WaitGroup, bucket nats.KeyValue, name string) error {
	defer wg.Done()

	w := &worker{name: name}

	opts := []election.Option{
		election.OnWon(w.win),
		election.OnLost(w.lost),
		election.OnCampaign(func(s election.State) {
			log.Printf("%s: Campaigning while in state %v", name, s)
		}),
		election.WithDebug(func(format string, v ...interface{}) {
			log.Printf(fmt.Sprintf("%s: %s", name, format), v...)
		}),
	}

	if os.Getenv("NOSPLAY") == "1" {
		opts = append(opts, election.WithoutSplay())
	}

	e, err := election.NewElection(name, "demo", bucket, opts...)
	if err != nil {
		log.Printf("Creating election failed: %s", err)
		return err
	}

	wg.Add(1)
	go w.run(ctx, wg)

	log.Printf("%s: starting election", name)

	err = e.Start(ctx)
	if err != nil {
		log.Printf("%s: election did not start: %s", name, err)
	}

	return err
}

func main() {
	nc, err := natscontext.Connect(os.Getenv("CONTEXT"), nats.MaxReconnects(-1))
	if err != nil {
		log.Printf("Could not load NATS context: %s", err)
		os.Exit(1)
	}

	js, err := nc.JetStream()
	if err != nil {
		log.Printf("Could not load JetStream context: %s", err)
		os.Exit(1)
	}

	bucket, err := js.KeyValue("DEMO_ELECTION")
	if err != nil {
		log.Printf("Could not load bucket DEMO_ELECTION: %s", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}

	for i := 1; i < 10; i++ {
		wg.Add(1)
		go startWorker(ctx, wg, bucket, fmt.Sprintf("worker %d", i))
	}

	wg.Wait()
}
