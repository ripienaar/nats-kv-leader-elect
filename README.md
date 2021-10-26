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

## Limitations?

We have some hard-coded limits just to keep things sane:

 * Shortest bucket TTL is 30 seconds
 * Longest bucket TTL is 1 hour
 * Shortest campaign interval is 5 seconds
 * Shortest delta from campaign interval to bucket TTL is 5 seconds

When a leadership is gained the leader is only notified that it is won after the campaign interval - 75% of the TTL - 
this ensures that any previous leader had a chance to stand down.

## Usage?

```go
kv, _ = js.CreateKeyValue(&nats.KeyValueConfig{
    Bucket: "LEADER_ELECTION",
    TTL:    60 * time.Second,
})

elect, _ := NewElection("member 1", "election", kv,
	OnLeaderGained(handleBecomingLeader),
    OnLeaderLost(handleLosingLeadership)))

// blocks until stopped, calls the handleBecomingLeader() and handleLosingLeadership() functions on change
elect.Start(ctx)
```

## Contact?

R.I.Pienaar / @ripienaar / rip@devco.net
