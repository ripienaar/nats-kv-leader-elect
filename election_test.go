package election

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestLeader(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NatsKVElection")
}

var _ = Describe("NATS KV Leader Election", func() {
	var (
		srv      *server.Server
		nc       *nats.Conn
		js       nats.KeyValueManager
		kv       nats.KeyValue
		err      error
		debugger func(f string, a ...interface{})
	)

	BeforeEach(func() {
		skipValidate = false
		srv, nc = startJSServer(GinkgoT())
		js, err = nc.JetStream()
		Expect(err).ToNot(HaveOccurred())

		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket: "LEADER_ELECTION",
			TTL:    1500 * time.Millisecond,
		})
		Expect(err).ToNot(HaveOccurred())
		debugger = func(f string, a ...interface{}) {
			fmt.Fprintf(GinkgoWriter, fmt.Sprintf("%s\n", f), a...)
		}
	})

	AfterEach(func() {
		nc.Close()
		srv.Shutdown()
		srv.WaitForShutdown()
		if srv.StoreDir() != "" {
			os.RemoveAll(srv.StoreDir())
		}
	})

	Describe("Election", func() {
		It("Should validate the TTL", func() {
			kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
				Bucket: "LE",
				TTL:    time.Second,
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = NewElection("test", "test.key", kv)
			Expect(err).To(MatchError("bucket TTL should be 30 seconds or more"))

			err = js.DeleteKeyValue("LE")
			Expect(err).ToNot(HaveOccurred())

			kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
				Bucket: "LE",
				TTL:    24 * time.Hour,
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = NewElection("test", "test.key", kv)
			Expect(err).To(MatchError("bucket TTL should be less than or equal to 1 hour"))
		})

		It("Should correctly manage leadership", func() {
			var (
				wins      = 0
				lost      = 0
				active    = 0
				maxActive = 0
				other     = 0
				wg        = &sync.WaitGroup{}
				mu        = sync.Mutex{}
			)

			skipValidate = true

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			worker := func(wg *sync.WaitGroup, i int, key string) {
				defer wg.Done()

				winCb := func() {
					mu.Lock()
					wins++
					active++
					if active > maxActive {
						maxActive = active
					}
					act := active
					mu.Unlock()

					debugger("%d became leader with %d active leaders", i, act)
				}

				lostCb := func() {
					mu.Lock()
					lost++
					active--
					mu.Unlock()
					debugger("%d lost leadership", i)
				}

				elect, err := NewElection(fmt.Sprintf("member %d", i), key, kv,
					OnWon(winCb),
					OnLost(lostCb),
					WithDebug(debugger))
				Expect(err).ToNot(HaveOccurred())

				err = elect.Start(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			for i := 0; i < 10; i++ {
				wg.Add(1)
				go worker(wg, i, "election")
			}

			// make sure another election is not interrupted
			otherWorker := func(wg *sync.WaitGroup) {
				defer wg.Done()

				elect, err := NewElection("other", "other", kv,
					OnWon(func() {
						mu.Lock()
						debugger("other gained leader")
						other++
						mu.Unlock()
					}),
					OnLost(func() {
						mu.Lock()
						Fail("Other election was lost")
						mu.Unlock()
					}))
				Expect(err).ToNot(HaveOccurred())

				err = elect.Start(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			wg.Add(2)
			go otherWorker(wg)
			go otherWorker(wg)

			// test failure scenarios, either the key gets deleted to allow a Create() to succeed
			// or it gets corrupted by putting a item in the key that would therefore change the sequence
			// causing next campaign by the leader to fail. The leader will stand-down, all campaigns will
			// fail until the corruption is removed by the MaxAge limit
			kills := 0
			for {
				if ctxSleep(ctx, 2*time.Second) != nil {
					break
				}

				kills++
				if kills%3 == 0 {
					debugger("deleting key")
					Expect(kv.Delete("election")).ToNot(HaveOccurred())
				} else {
					debugger("corrupting key")
					_, err := kv.Put("election", nil)
					Expect(err).ToNot(HaveOccurred())
				}
			}

			wg.Wait()

			mu.Lock()
			defer mu.Unlock()

			// check we had enough keys and wins etc to have tested all scenarios
			if kills < 5 {
				Fail(fmt.Sprintf("had %d kills", kills))
			}
			if wins < 5 {
				Fail(fmt.Sprintf("won only %d elections for %d kills", wins, kills))
			}
			if lost < 5 {
				Fail(fmt.Sprintf("lost only %d elections", lost))
			}
			if maxActive > 1 {
				Fail(fmt.Sprintf("Had %d leaders", maxActive))
			}
		})
	})
})

func startJSServer(t GinkgoTInterface) (*server.Server, *nats.Conn) {
	t.Helper()

	d, err := ioutil.TempDir("", "jstest")
	if err != nil {
		t.Fatalf("temp dir could not be made: %s", err)
	}

	opts := &server.Options{
		JetStream: true,
		StoreDir:  d,
		Port:      -1,
		Host:      "localhost",
		LogFile:   "/dev/stdout",
		Trace:     true,
	}

	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal("server start failed: ", err)
	}

	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Error("nats server did not start")
	}

	nc, err := nats.Connect(s.ClientURL(), nats.UseOldRequestStyle())
	if err != nil {
		t.Fatalf("client start failed: %s", err)
	}

	return s, nc
}
