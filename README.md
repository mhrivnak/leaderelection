# Leader Election

This repository implements Leader For Life, an alternative to lease-based
leader election, as a proof of concept.

## Background

Simple leader election, unless you're using a concensus algorithm (like raft),
very often involves having all the candidates try to create the same record
(referred to as a "lock") in a data store that enforces uniqueness constraints.
Often it's a table in a database, but it can just as easily be the Kubernetes
API.

Leases provide a way to indirectly observe whether the leader still exists.
The leader must renew its lease, usually by updating a timestamp in its lock
record. If it fails to do so, it is **presumed dead**, and a new election takes
place. If the leader is in fact still alive, it is expected to gracefully step
down.

A variety of factors can cause a leader to fail at updating its lease, but
continue acting as the leader before succeeding at stepping down. Thus there
can be an opportunity for two leaders to exist concurrently for a brief period
of time.

## Leader for Life

Kubernetes enables direct observation of the leader's lifecycle. In a simple
example, the leader can be defined as a Pod, and its lock record can be
automatically removed when the Pod is removed.

In the "leader for life" approach, a specific Pod is the leader. Once
established (by creating a lock record), the Pod is the leader until it is
destroyed. There is no possibility for multiple pods to think they are the
leader at the same time. The leader does not need to renew a lease, consider
stepping down, or do anything related to election activity once it becomes the
leader.

### Simplified Algorithm

1. At startup, our operator process attempts to create a ConfigMap with a known
   name (perhaps "myapp-lock"). The ConfigMap will have its OwnerReference set
to our operator's Pod.
2. If the create fails because a ConfigMap with that name already exists, that
   means a leader exists. Our operator will periodically continue trying to
create the ConfigMap until it eventually succeeds.
3. Once our operator process becomes the leader by successfully creating a
   ConfigMap, it has no further work to do with respect to elections. There is
no lease renewal, and certainly no stepping down to worry about.
4. When our operator's Pod eventually gets removed for some reason, the garbage
   collector will also remove its ConfigMap.
5. With the ConfigMap gone, another candidate process is able to create its own
   and become the new leader.

### Usage

Because of its simplicity, leader-for-life election can be used by any process
by making a single blocking call. For example:

```golang
package main

import (
    "github.com/mhrivnak/leaderelection/pkg/leader"
    "github.com/sirupsen/logrus"
)

func main() {
    err := leader.Become("myapp-lock")
    if err != nil {
        logrus.Fatal(err.Error())
    }
    ...
    // do whatever else your app does
}

```

## client-go leaderelection

Lease-based leader election is available [in
client-go](https://godoc.org/k8s.io/client-go/tools/leaderelection). Each candidate
tries to create a ConfigMap that contains its lease information. If a lease
expires, the other candidates can hold a new election. In such a case, if the
missing leader is still alive, it is expected to step down.

This implementation pre-dates the availability of owner references and garbage
collection in Kubernetes.

### Downsides

There is a possibility for two leaders to coexist for a brief time. At the same
time a leader may fail to renew its lease, it may be succeeding at changing
other kinds of state in the cluster, and it may not be able to completely cease
that activity before its lease expires. (clock skew, system load, API latency,
network latency, and other factorys may all contribute) If even a brief period
occurs during which the old leader and new leader are both active, that could
cause problems for some systems.

There is additional runtime complexity in lease renewal. While not a huge
burden, it is non-trivial for a leader to continuously renew its lease, and to
maintain the ability to step down at any moment, all while doing whatever its
normal work is. These are solvable problems, but leader-for-life is a simpler
approach that does not have these requirements.

### Upsides

Lease-based leader election may be well-suited to use cases that cannot depend
on garbage collection and owner references.

In a case where the leader has become stuck, but has not exited, leases enable
a new election to proceed.

## Enhancements

There are a number of potential enhancements to the leader-for-life approach,
including:
* Make the polling time configurable.
* Use a different object than a ConfigMap, such as a dedicated CRD, or the [new
  lease
object](https://github.com/kubernetes/kubernetes/blob/0950084137/staging/src/k8s.io/api/coordination/v1beta1/types.go#L27).
* Watch the lock object directly, or the leader's Pod, to get an event as soon
  as it disappears.
