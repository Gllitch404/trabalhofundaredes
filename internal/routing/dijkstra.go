package routing

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// LSARecord represents a valid LSA entry in the LSDB for graph construction.
type LSARecord struct {
	OriginIP   string
	Sequence   int
	Links      map[string]int
	ReceivedAt time.Time
}

// Route represents a single entry in the routing table.
type Route struct {
	Destination string
	TotalCost   int
	NextHop     string
}

type ChangeKind string

const (
	RouteAdded    ChangeKind = "INCLUIDA"
	RouteRemoved  ChangeKind = "REMOVIDA"
	RouteModified ChangeKind = "MODIFICADA"
)

// RouteChange describes a change between an old and a new routing table.
type RouteChange struct {
	Kind        ChangeKind
	Destination string
	OldRoute    *Route
	NewRoute    *Route
}

// BuildGraph builds a directed adjacency list from the LSDB.
// An edge u -> v with cost c(u, v) is included only when both u and v have valid LSAs
// in the LSDB recognizing the link between them.
// For edges leaving selfIP, v must also be in activeNeighbors.
func BuildGraph(selfIP string, lsdb map[string]LSARecord, activeNeighbors map[string]bool) map[string]map[string]int {
	graph := make(map[string]map[string]int)
	for u, uRec := range lsdb {
		for v, costUV := range uRec.Links {
			if costUV <= 0 {
				continue
			}
			vRec, ok := lsdb[v]
			if !ok {
				continue
			}
			costVU, bidirectional := vRec.Links[u]
			if !bidirectional || costVU <= 0 {
				continue
			}
			if u == selfIP && !activeNeighbors[v] {
				continue
			}
			if graph[u] == nil {
				graph[u] = make(map[string]int)
			}
			graph[u][v] = costUV
		}
	}
	return graph
}

// ComputeDijkstra computes shortest paths from selfIP using a custom Dijkstra implementation.
// Equal-cost paths choose a next hop at random. selfIP is never included in the returned table.
func ComputeDijkstra(selfIP string, lsdb map[string]LSARecord, activeNeighbors map[string]bool, rng *rand.Rand) map[string]Route {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	graph := BuildGraph(selfIP, lsdb, activeNeighbors)
	dist := make(map[string]int)
	nextHops := make(map[string][]string)
	visited := make(map[string]bool)

	nodes := make(map[string]struct{})
	nodes[selfIP] = struct{}{}
	for u, nbrs := range graph {
		nodes[u] = struct{}{}
		for v := range nbrs {
			nodes[v] = struct{}{}
		}
	}

	for n := range nodes {
		dist[n] = math.MaxInt
	}
	dist[selfIP] = 0

	for {
		// Find unvisited node with minimum distance.
		u := ""
		bestDist := math.MaxInt
		for n := range nodes {
			if !visited[n] && dist[n] < bestDist {
				bestDist = dist[n]
				u = n
			}
		}
		if u == "" || bestDist == math.MaxInt {
			break
		}
		visited[u] = true

		for v, weight := range graph[u] {
			if visited[v] {
				continue
			}
			alt := dist[u] + weight
			if alt < dist[v] {
				dist[v] = alt
				if u == selfIP {
					nextHops[v] = []string{v}
				} else {
					nhCopy := make([]string, len(nextHops[u]))
					copy(nhCopy, nextHops[u])
					nextHops[v] = nhCopy
				}
			} else if alt == dist[v] {
				var candidates []string
				if u == selfIP {
					candidates = []string{v}
				} else {
					candidates = nextHops[u]
				}
				for _, nh := range candidates {
					if !containsString(nextHops[v], nh) {
						nextHops[v] = append(nextHops[v], nh)
					}
				}
			}
		}
	}

	table := make(map[string]Route)
	for dest, d := range dist {
		if dest == selfIP || d == math.MaxInt || len(nextHops[dest]) == 0 {
			continue
		}
		chosenNH := nextHops[dest][rng.Intn(len(nextHops[dest]))]
		table[dest] = Route{
			Destination: dest,
			TotalCost:   d,
			NextHop:     chosenNH,
		}
	}
	return table
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

// DiffRoutes computes added, removed, and modified routes between oldTable and newTable.
func DiffRoutes(oldTable, newTable map[string]Route) []RouteChange {
	seen := make(map[string]struct{})
	for k := range oldTable {
		seen[k] = struct{}{}
	}
	for k := range newTable {
		seen[k] = struct{}{}
	}

	dests := make([]string, 0, len(seen))
	for k := range seen {
		dests = append(dests, k)
	}
	sort.Strings(dests)

	var changes []RouteChange
	for _, d := range dests {
		oldR, hasOld := oldTable[d]
		newR, hasNew := newTable[d]
		switch {
		case !hasOld && hasNew:
			r := newR
			changes = append(changes, RouteChange{
				Kind:        RouteAdded,
				Destination: d,
				NewRoute:    &r,
			})
		case hasOld && !hasNew:
			r := oldR
			changes = append(changes, RouteChange{
				Kind:        RouteRemoved,
				Destination: d,
				OldRoute:    &r,
			})
		case hasOld && hasNew && (oldR.TotalCost != newR.TotalCost || oldR.NextHop != newR.NextHop):
			or := oldR
			nr := newR
			changes = append(changes, RouteChange{
				Kind:        RouteModified,
				Destination: d,
				OldRoute:    &or,
				NewRoute:    &nr,
			})
		}
	}
	return changes
}
