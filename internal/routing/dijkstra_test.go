package routing

import (
	"math/rand"
	"testing"
	"time"
)

func TestDijkstraAsymmetricAndBidirectionalCheck(t *testing.T) {
	now := time.Now()
	// R1 --(10/5)-- R2 --(3/3)-- R4
	//  \--(2/2)-- R3 --(20/20)-- R4
	// Also R2 -> R5 is unidirectional (R5 doesn't list R2), so R5 must NOT be reachable.
	lsdb := map[string]LSARecord{
		"10.0.0.1": {
			OriginIP: "10.0.0.1", Sequence: 1, ReceivedAt: now,
			Links: map[string]int{"10.0.0.2": 10, "10.0.0.3": 2},
		},
		"10.0.0.2": {
			OriginIP: "10.0.0.2", Sequence: 1, ReceivedAt: now,
			Links: map[string]int{"10.0.0.1": 5, "10.0.0.4": 3, "10.0.0.5": 1},
		},
		"10.0.0.3": {
			OriginIP: "10.0.0.3", Sequence: 1, ReceivedAt: now,
			Links: map[string]int{"10.0.0.1": 2, "10.0.0.4": 20},
		},
		"10.0.0.4": {
			OriginIP: "10.0.0.4", Sequence: 1, ReceivedAt: now,
			Links: map[string]int{"10.0.0.2": 3, "10.0.0.3": 20},
		},
		"10.0.0.5": {
			OriginIP: "10.0.0.5", Sequence: 1, ReceivedAt: now,
			Links: map[string]int{}, // does NOT recognize 10.0.0.2
		},
	}
	active := map[string]bool{"10.0.0.2": true, "10.0.0.3": true}
	rng := rand.New(rand.NewSource(42))

	table := ComputeDijkstra("10.0.0.1", lsdb, active, rng)

	if _, hasSelf := table["10.0.0.1"]; hasSelf {
		t.Fatal("routing table should not contain self")
	}
	if _, hasR5 := table["10.0.0.5"]; hasR5 {
		t.Fatal("unidirectional link to 10.0.0.5 should not be usable")
	}
	r4, ok := table["10.0.0.4"]
	if !ok {
		t.Fatal("expected route to 10.0.0.4")
	}
	if r4.TotalCost != 13 || r4.NextHop != "10.0.0.2" {
		t.Fatalf("expected 10.0.0.4 cost=13 nextHop=10.0.0.2, got %+v", r4)
	}
}

func TestDijkstraEqualCostRandomSelection(t *testing.T) {
	now := time.Now()
	lsdb := map[string]LSARecord{
		"10.0.0.1": {OriginIP: "10.0.0.1", Sequence: 1, ReceivedAt: now, Links: map[string]int{"10.0.0.2": 5, "10.0.0.3": 5}},
		"10.0.0.2": {OriginIP: "10.0.0.2", Sequence: 1, ReceivedAt: now, Links: map[string]int{"10.0.0.1": 5, "10.0.0.4": 5}},
		"10.0.0.3": {OriginIP: "10.0.0.3", Sequence: 1, ReceivedAt: now, Links: map[string]int{"10.0.0.1": 5, "10.0.0.4": 5}},
		"10.0.0.4": {OriginIP: "10.0.0.4", Sequence: 1, ReceivedAt: now, Links: map[string]int{"10.0.0.2": 5, "10.0.0.3": 5}},
	}
	active := map[string]bool{"10.0.0.2": true, "10.0.0.3": true}

	seenNextHops := map[string]bool{}
	for i := int64(1); i <= 50; i++ {
		rng := rand.New(rand.NewSource(i))
		table := ComputeDijkstra("10.0.0.1", lsdb, active, rng)
		seenNextHops[table["10.0.0.4"].NextHop] = true
	}
	if !seenNextHops["10.0.0.2"] || !seenNextHops["10.0.0.3"] {
		t.Fatalf("expected both equal-cost next hops to be chosen randomly, got %v", seenNextHops)
	}
}

func TestDiffRoutes(t *testing.T) {
	oldT := map[string]Route{
		"10.0.0.2": {Destination: "10.0.0.2", TotalCost: 10, NextHop: "10.0.0.2"},
		"10.0.0.3": {Destination: "10.0.0.3", TotalCost: 5, NextHop: "10.0.0.3"},
	}
	newT := map[string]Route{
		"10.0.0.2": {Destination: "10.0.0.2", TotalCost: 15, NextHop: "10.0.0.4"},
		"10.0.0.4": {Destination: "10.0.0.4", TotalCost: 7, NextHop: "10.0.0.4"},
	}
	changes := DiffRoutes(oldT, newT)
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}
	if changes[0].Kind != RouteModified || changes[1].Kind != RouteRemoved || changes[2].Kind != RouteAdded {
		t.Fatalf("unexpected diff kinds: %+v", changes)
	}
}
