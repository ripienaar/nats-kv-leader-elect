## What?

A Leader Election system that uses keys in a NATS Key-Value Store to perform leader election.

## How?

NATS KV Buckets have a TTL, creating a bucket with a TTL of 1 minute will imply that a leader has to maintain his
leadership by updating the bucket more frequently than 1 minute.

The NATS KV Interface has a `Create()` function that will only succeed if the key does not already exist, thus a 
worker who is campaigning for leadership will regularly attempt to create data in the key, whoever manages to do
this becomes the leader.

A leader will then regularly, around 75% of TTL time, do a `Update()` on the key ensuring that the sequence number
is the sequence number of his last `Update()`, as long as this series is maintained he knows he is still the leader.
Failure to `Update()` is a leadership loss.

If the leader fails to update the key within TTL, the key will expire and one of the campaigners will have a successful
`Create()` call, so leadership is switched.

## Limitations?

We have some hard-coded limits just to keep things from being too aggressive:

 * Shortest bucket TTL is 30 seconds
 * Longest bucket TTL is 1 hour
 * Shortest campaign interval is 5 seconds
 * Shortest delta from campaign interval to bucket TTL is 5 seconds

When a leadership is gained the leader is only notified that it is won after the campaign interval - 75% of the TTL - 
this ensures that any previous leader had a chance to stand down.

Additionally, a back-off is supported that can slow down campaigns by non leader candidates. Using this the TTL can be
kept low, leadership switches be handled without a long sleep and campaigns by candidates do not need to be too aggressive.
It also implies that if a leadership is lost that leader will, initially, re-campaign aggressively, in practise this results
in leadership staying relatively stable in periods of network uncertainty or cluster reboots.

## Usage?

```nohighlight
$ nats kv add --ttl 5m ELECTIONS 
```

```go
kv, _ := js.KeyValue("ELECTIONS")

elect, _ := NewElection("member 1", "election", kv,
	OnWon(handleBecomingLeader),
	OnLost(handleLosingLeadership)))

// blocks until stopped, calls the handleBecomingLeader() and handleLosingLeadership() functions on change
elect.Start(ctx)
```

## Demo?

We have a demo program in `cmd` directory, this requires a `DEMO_ELECTION` bucket:

```nohighlight
$ nats kv add --ttl 31s DEMO_ELECTION 
```

It also requires a `nats context` setup that connect to your nats network, use the `nats context add` CLI to do that. Without
setting `CONTEXT` like below it will use your default selected context, same as the CLI.

```nohighlight
$ go install github.com/ripienaar/nats-kv-leader-elect/cmd/election@main
$ CONTEXT=election election
```

To test failover functions, delete the key being campaigned against:

```nohighlight
$ nats kv del DEMO_ELECTION demo
```

A few environment variables can be set to influence the test program:

|Environment|Description|Default|
|-----------|-----------|-------|
|NOSPLAY    |Set to 1 to disable initial sleep that spreads campaigners|0|
|KEY        |The key to campaign against|demo|
|CONTEXT    |The nats context to connect with, when empty uses the default selected one||
|WORKERS    |How many workers to start|10|


## Contact?

R.I.Pienaar / @ripienaar / rip@devco.net
