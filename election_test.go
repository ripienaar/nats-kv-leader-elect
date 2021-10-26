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

func TestChoria(t *testing.T) {
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
			TTL:    2 * time.Second,
		})
		Expect(err).ToNot(HaveOccurred())
		debugger = func(f string, a ...interface{}) {
			fmt.Fprintf(GinkgoWriter, f, a...)
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
				wg        = &sync.WaitGroup{}
				mu        = sync.Mutex{}
			)

			skipValidate = true

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			worker := func(wg *sync.WaitGroup, i int) {
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

					debugger("%d became leader with %d active leaders\n", i, act)
				}

				lostCb := func() {
					mu.Lock()
					lost++
					active--
					mu.Unlock()
					debugger("%d lost leadership\n", i)
				}

				elect, err := NewElection(fmt.Sprintf("member %d", i), "election", kv,
					OnWon(winCb),
					OnLost(lostCb),
					WithDebug(debugger))
				Expect(err).ToNot(HaveOccurred())

				err = elect.Start(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			for i := 0; i < 10; i++ {
				wg.Add(1)
				go worker(wg, i)
			}

			kills := 0
			for {
				if ctxSleep(ctx, 2*time.Second) != nil {
					break
				}
				kills++
				val, _ := kv.Get("election")
				if val != nil {
					debugger("current leader is %q\n", val.Value())
				}
				debugger("deleting key\n")
				Expect(kv.Delete("election")).ToNot(HaveOccurred())
			}

			wg.Wait()

			mu.Lock()
			defer mu.Unlock()

			if kills < 5 {
				Fail(fmt.Sprintf("had only %d kills", kills))
			}
			if wins < kills {
				Fail(fmt.Sprintf("won only %d elections", wins))
			}
			if lost < kills {
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
