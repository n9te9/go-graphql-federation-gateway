package graph

import (
	"container/heap"
	"fmt"
)

// GraphNode represents a node in the weighted directed graph.
// The node corresponds to a specific field in a specific subgraph.
// Key format: "{SubGraphName}:{typeName}.{fieldName}" or "{SubGraphName}:{typeName}" for type-level nodes.
type GraphNode struct {
	ID        string         // Node identifier (e.g., "SubGraphA:Review.product")
	SubGraph  *SubGraphV2    // The subgraph this node belongs to
	TypeName  string         // Type name (e.g., "Review")
	FieldName string         // Field name (e.g., "product"), empty for type-level nodes
	Edges     map[string]int // Adjacent nodes and their weights
	ShortCut  map[string]int // Shortcut edges from @provides (static route cache; value is always 0)
}

// WeightedDirectedGraph is a weighted directed graph representing inter-subgraph dependencies.
type WeightedDirectedGraph struct {
	Nodes map[string]*GraphNode
}

// NewWeightedDirectedGraph creates an empty weighted directed graph.
func NewWeightedDirectedGraph() *WeightedDirectedGraph {
	return &WeightedDirectedGraph{
		Nodes: make(map[string]*GraphNode),
	}
}

// AddNode adds a node to the graph. If the node already exists, it is returned as-is.
func (g *WeightedDirectedGraph) AddNode(id string, subGraph *SubGraphV2, typeName, fieldName string) *GraphNode {
	if existing, ok := g.Nodes[id]; ok {
		return existing
	}
	node := &GraphNode{
		ID:        id,
		SubGraph:  subGraph,
		TypeName:  typeName,
		FieldName: fieldName,
		Edges:     make(map[string]int),
		ShortCut:  make(map[string]int),
	}
	g.Nodes[id] = node
	return node
}

// AddEdge adds a directed edge from the node with srcID to the node with dstID.
// weight=0 means same-subgraph traversal; weight=1 means cross-subgraph traversal.
func (g *WeightedDirectedGraph) AddEdge(srcID, dstID string, weight int) {
	src, ok := g.Nodes[srcID]
	if !ok {
		return
	}
	// Always update with the minimum weight (prefer 0-cost paths).
	if existing, exists := src.Edges[dstID]; !exists || weight < existing {
		src.Edges[dstID] = weight
	}
}

// AddShortCut adds a @provides shortcut edge (weight=0) to the node srcID.
func (g *WeightedDirectedGraph) AddShortCut(srcID, dstID string) {
	src, ok := g.Nodes[srcID]
	if !ok {
		return
	}
	src.ShortCut[dstID] = 0
}

// NodeKey returns the graph node key for a given subgraph, type, and field.
// When fieldName is empty, returns a type-level key.
func NodeKey(subGraphName, typeName, fieldName string) string {
	if fieldName == "" {
		return fmt.Sprintf("%s:%s", subGraphName, typeName)
	}
	return fmt.Sprintf("%s:%s.%s", subGraphName, typeName, fieldName)
}

// -----------------------------------------------------------------------
// Dijkstra priority queue implementation
// -----------------------------------------------------------------------

// dijkstraItem is an element in the priority queue.
type dijkstraItem struct {
	nodeID string
	cost   int
	index  int // maintained by heap.Interface
}

// dijkstraPQ implements heap.Interface for a min-heap of dijkstraItem.
type dijkstraPQ []*dijkstraItem

func (pq dijkstraPQ) Len() int           { return len(pq) }
func (pq dijkstraPQ) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
func (pq dijkstraPQ) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *dijkstraPQ) Push(x any) {
	n := len(*pq)
	item := x.(*dijkstraItem)
	item.index = n
	*pq = append(*pq, item)
}
func (pq *dijkstraPQ) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

// DijkstraResult contains the shortest path information from a Dijkstra run.
type DijkstraResult struct {
	// Dist maps nodeID -> minimum cost to reach that node from any entry point.
	Dist map[string]int
	// Prev maps nodeID -> predecessor nodeID (for path reconstruction).
	Prev map[string]string
}

// Dijkstra runs Dijkstra's algorithm on the graph starting from the given entry points (cost=0).
// entryPoints is a list of node IDs where traversal begins.
func (g *WeightedDirectedGraph) Dijkstra(entryPoints []string) *DijkstraResult {
	dist := make(map[string]int, len(g.Nodes))
	prev := make(map[string]string, len(g.Nodes))

	const inf = int(^uint(0) >> 1)
	for id := range g.Nodes {
		dist[id] = inf
	}

	pq := &dijkstraPQ{}
	heap.Init(pq)

	for _, ep := range entryPoints {
		if _, ok := g.Nodes[ep]; ok {
			dist[ep] = 0
			heap.Push(pq, &dijkstraItem{nodeID: ep, cost: 0})
		}
	}

	for pq.Len() > 0 {
		item := heap.Pop(pq).(*dijkstraItem)
		u := item.nodeID
		currentCost := item.cost

		if currentCost > dist[u] {
			continue // stale entry
		}

		node := g.Nodes[u]

		// Explore normal edges
		for vID, w := range node.Edges {
			newCost := dist[u] + w
			if newCost < dist[vID] {
				dist[vID] = newCost
				prev[vID] = u
				heap.Push(pq, &dijkstraItem{nodeID: vID, cost: newCost})
			}
		}

		// Explore shortcut edges (always weight 0)
		for vID := range node.ShortCut {
			newCost := dist[u] // + 0
			existingCost, exists := dist[vID]
			if !exists || newCost < existingCost {
				dist[vID] = newCost
				prev[vID] = u
				heap.Push(pq, &dijkstraItem{nodeID: vID, cost: newCost})
			}
		}
	}

	return &DijkstraResult{Dist: dist, Prev: prev}
}

// ReconstructPath returns the path from any entry point to dstID using the prev map.
// Returns an empty slice if dstID is unreachable.
func (r *DijkstraResult) ReconstructPath(dstID string) []string {
	const inf = int(^uint(0) >> 1)
	if cost, ok := r.Dist[dstID]; !ok || cost == inf {
		return nil
	}

	var path []string
	visited := make(map[string]bool)
	for cur := dstID; cur != ""; {
		if visited[cur] {
			break
		}
		visited[cur] = true
		path = append([]string{cur}, path...)
		prev, hasPrev := r.Prev[cur]
		if !hasPrev {
			break
		}
		cur = prev
	}
	return path
}

// BuildGraph constructs the weighted directed graph from the subgraphs' schema metadata.
// This is called once during NewSuperGraphV2 to pre-compute the graph.
//
// Graph construction rules:
//   - For each subgraph, add a type-level node for every entity/object type.
//   - For each field in a type, add a field-level node under that subgraph.
//   - Same-subgraph type → field edge:  weight 0
//   - Cross-subgraph type → type edge (via @key):  weight 1
//   - @provides fields add ShortCut edges (weight 0) from the field node to the provided field nodes.
func BuildGraph(subGraphs []*SubGraphV2) *WeightedDirectedGraph {
	g := NewWeightedDirectedGraph()

	// First pass: create all type-level and field-level nodes.
	for _, sg := range subGraphs {
		for typeName, entity := range sg.GetEntities() {
			typeKey := NodeKey(sg.Name, typeName, "")
			g.AddNode(typeKey, sg, typeName, "")

			for fieldName, field := range entity.Fields {
				fieldKey := NodeKey(sg.Name, typeName, fieldName)
				g.AddNode(fieldKey, sg, typeName, fieldName)

				// type → field (same subgraph, weight 0)
				g.AddEdge(typeKey, fieldKey, 0)

				// @provides: field node → provided field node (shortcut, weight 0)
				// Store placeholder keys; they are resolved in the third pass.
				for _, providedField := range field.Provides {
					// placeholder format: "{sgName}:{typeName}.{fieldName}:{providedField}"
					placeholderKey := fmt.Sprintf("%s:%s.%s:%s", sg.Name, typeName, fieldName, providedField)
					g.AddShortCut(fieldKey, placeholderKey)
				}
			}
		}
	}

	// Second pass: add cross-subgraph edges based on @key directives and field ownership.
	// For each entity that appears in multiple subgraphs, connect the type nodes.
	entitySubGraphs := make(map[string][]*SubGraphV2) // typeName -> subgraphs that define it
	for _, sg := range subGraphs {
		for typeName := range sg.GetEntities() {
			entitySubGraphs[typeName] = append(entitySubGraphs[typeName], sg)
		}
	}

	for typeName, sgs := range entitySubGraphs {
		if len(sgs) < 2 {
			continue
		}
		// Add cross edges between all pairs (both directions, weight 1).
		for i, sgA := range sgs {
			for _, sgB := range sgs[i+1:] {
				keyA := NodeKey(sgA.Name, typeName, "")
				keyB := NodeKey(sgB.Name, typeName, "")
				g.AddEdge(keyA, keyB, 1)
				g.AddEdge(keyB, keyA, 1)
			}
		}
	}

	// Third pass: resolve @provides ShortCut placeholder keys to real field node keys.
	// Replace "SubGraph:TypeName.FieldName:ProvidedField" with the actual node key
	// that corresponds to the provided field in another subgraph.
	g.resolveProvideShortCuts(subGraphs)

	return g
}

// resolveProvideShortCuts replaces placeholder shortcut keys with real graph node keys.
// A @provides(fields: "name") on field `Review.product: Product` in SubGraphA means
// that when fetching via SubGraphA.Review.product, the field SubGraphB.Product.name
// can be resolved without an extra cross-subgraph hop.
func (g *WeightedDirectedGraph) resolveProvideShortCuts(subGraphs []*SubGraphV2) {
	// Build a reverse lookup: "TypeName.fieldName" -> subgraph that non-externally owns it.
	// We iterate all subgraph schemas to find the authoritative owner (no @external).
	fieldOwner := make(map[string]*SubGraphV2)
	for _, sg := range subGraphs {
		for typeName, entity := range sg.GetEntities() {
			for fieldName := range entity.Fields {
				ownerKey := fmt.Sprintf("%s.%s", typeName, fieldName)
				// Use best-effort: the subgraph that has the node in the graph
				// and a non-placeholder node key is the owner.
				realNodeKey := NodeKey(sg.Name, typeName, fieldName)
				if _, exists := g.Nodes[realNodeKey]; exists {
					if _, already := fieldOwner[ownerKey]; !already {
						fieldOwner[ownerKey] = sg
					}
				}
			}
		}
	}

	// Resolve placeholder shortcut keys.
	// Placeholder format: "{sgName}:{typeName}.{fieldName}:{providedField}"
	// We want to map the "{providedField}" to its real node across all subgraphs.
	for _, node := range g.Nodes {
		if len(node.ShortCut) == 0 {
			continue
		}

		newShortCuts := make(map[string]int)
		for placeholderKey := range node.ShortCut {
			// Try to find a real node in the graph that matches the provided field.
			// Look for nodes where SubGraph != node's subgraph and FieldName == providedField.
			resolved := false
			// Extract the provided field name from placeholder: last segment after the final ":"
			// Placeholder format is always "{sgName}:{typeName}.{fieldName}:{providedField}",
			// so the last segment is the providedField.
			lastColon := -1
			for i := len(placeholderKey) - 1; i >= 0; i-- {
				if placeholderKey[i] == ':' {
					lastColon = i
					break
				}
			}
			providedFieldName := placeholderKey[lastColon+1:]

			for realKey, realNode := range g.Nodes {
				if realNode.FieldName == providedFieldName && realNode.SubGraph.Name != node.SubGraph.Name {
					newShortCuts[realKey] = 0
					resolved = true
					break
				}
			}
			if !resolved {
				// Keep unresolved placeholder (won't match any traversal node).
				newShortCuts[placeholderKey] = 0
			}
		}
		node.ShortCut = newShortCuts
	}
}
