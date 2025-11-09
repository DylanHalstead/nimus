package nimbus

import (
	"strings"
)

// nodeType represents the type of node in the radix tree
type nodeType uint8

const (
	static   nodeType = iota // static path segment
	param                    // path parameter (:param)
	wildcard                 // catch-all (*path)
)

// node represents a node in the radix tree
type node struct {
	// Node properties
	nType    nodeType
	label    byte   // First character of the path segment (for quick matching)
	prefix   string // Common prefix for this node
	paramKey string // Parameter name (e.g., "id" for ":id")

	// Route information
	route *Route // Handler for this exact path (nil if not a complete route)

	// Children
	children   []*node // Static and param children
	paramChild *node   // Single param child (:param)
}

// tree represents a radix tree for a specific HTTP method
type tree struct {
	root *node
}

// newTree creates a new radix tree
func newTree() *tree {
	return &tree{
		root: &node{
			nType:    static,
			children: make([]*node, 0),
		},
	}
}

// insert adds a route to the tree
func (t *tree) insert(path string, route *Route) {
	// Normalize path
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	t.root.insert(path, route)
}

// insert recursively inserts a route into the tree
func (n *node) insert(path string, route *Route) {
	// Handle root path
	if path == "/" {
		n.route = route
		return
	}

	// Remove leading slash for processing
	path = strings.TrimPrefix(path, "/")

	// Find the next segment
	segmentEnd := strings.IndexByte(path, '/')
	var segment, remaining string

	if segmentEnd == -1 {
		// Last segment
		segment = path
		remaining = ""
	} else {
		segment = path[:segmentEnd]
		remaining = path[segmentEnd:]
	}

	// Determine node type for this segment
	var segType nodeType
	var paramKey string

	if len(segment) > 0 && segment[0] == ':' {
		segType = param
		paramKey = segment[1:] // Remove the ":"
	} else if len(segment) > 0 && segment[0] == '*' {
		segType = wildcard
		paramKey = segment[1:] // Remove the "*"
	} else {
		segType = static
	}

	// Handle parameter nodes
	if segType == param {
		if n.paramChild == nil {
			n.paramChild = &node{
				nType:    param,
				prefix:   segment,
				paramKey: paramKey,
				children: make([]*node, 0),
			}
		}

		if remaining == "" {
			n.paramChild.route = route
		} else {
			n.paramChild.insert(remaining, route)
		}
		return
	}

	// Handle static nodes
	// Look for existing child with matching prefix
	for _, child := range n.children {
		if child.nType != static {
			continue
		}

		// Check if prefixes match
		commonLen := longestCommonPrefix(segment, child.prefix)

		if commonLen == 0 {
			continue
		}

		// Found a matching child
		if commonLen == len(child.prefix) {
			// Child prefix is a prefix of our segment
			if commonLen == len(segment) {
				// Exact match - continue down the tree
				if remaining == "" {
					child.route = route
				} else {
					child.insert(remaining, route)
				}
			} else {
				// Our segment extends beyond child prefix
				newSegment := segment[commonLen:]
				child.insert("/"+newSegment+remaining, route)
			}
			return
		}

		// Need to split the existing child
		// Create a new parent node with the common prefix
		splitNode := &node{
			nType:    static,
			label:    child.label,
			prefix:   child.prefix[:commonLen],
			children: make([]*node, 0),
		}

		// Update the existing child to have the remaining prefix
		child.prefix = child.prefix[commonLen:]
		child.label = child.prefix[0]

		// Add the old child to the new parent
		splitNode.children = append(splitNode.children, child)

		// Replace old child with split node in parent's children
		for i, c := range n.children {
			if c == child {
				n.children[i] = splitNode
				break
			}
		}

		// Now insert into the split node
		if commonLen == len(segment) {
			// Exact match with common prefix
			if remaining == "" {
				splitNode.route = route
			} else {
				splitNode.insert(remaining, route)
			}
		} else {
			// Need to add another child
			newSegment := segment[commonLen:]
			splitNode.insert("/"+newSegment+remaining, route)
		}
		return
	}

	// No matching child found - create a new one
	newChild := &node{
		nType:    static,
		label:    segment[0],
		prefix:   segment,
		children: make([]*node, 0),
	}

	if remaining == "" {
		newChild.route = route
	} else {
		newChild.insert(remaining, route)
	}

	n.children = append(n.children, newChild)
}

// search finds a route in the tree and extracts path parameters
func (t *tree) search(path string) (*Route, map[string]string) {
	if path == "" {
		path = "/"
	}

	// Lazy allocation: don't allocate params map until we know we need it
	var params map[string]string
	route := t.root.search(path, &params)

	if route == nil {
		return nil, nil
	}

	// params will be nil for static routes (no allocation)
	return route, params
}

// search recursively searches for a route in the tree
func (n *node) search(path string, params *map[string]string) *Route {
	// Handle root path
	if path == "/" || path == "" {
		return n.route
	}

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Find the next segment
	segmentEnd := strings.IndexByte(path, '/')
	var segment, remaining string

	if segmentEnd == -1 {
		segment = path
		remaining = ""
	} else {
		segment = path[:segmentEnd]
		remaining = path[segmentEnd:]
	}

	// Try static children first (they have priority)
	for _, child := range n.children {
		if child.nType != static {
			continue
		}

		// Check if segment starts with child's prefix
		if strings.HasPrefix(segment, child.prefix) {
			if len(segment) == len(child.prefix) {
				// Exact match
				if remaining == "" {
					return child.route
				}
				return child.search(remaining, params)
			} else if len(segment) > len(child.prefix) {
				// Segment is longer - continue matching
				newPath := "/" + segment[len(child.prefix):] + remaining
				return child.search(newPath, params)
			}
		}
	}

	// Try parameter child
	if n.paramChild != nil {
		// Lazy allocate params map only when we actually have parameters (1 bucket = 8 capacity)
		if *params == nil {
			*params = make(map[string]string, 8)
		}
		(*params)[n.paramChild.paramKey] = segment

		if remaining == "" {
			return n.paramChild.route
		}
		return n.paramChild.search(remaining, params)
	}

	return nil
}

// longestCommonPrefix returns the length of the longest common prefix
func longestCommonPrefix(a, b string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}

	for i := 0; i < max; i++ {
		if a[i] != b[i] {
			return i
		}
	}

	return max
}

// collectRoutes gathers all routes from the tree (used for OpenAPI generation)
func (t *tree) collectRoutes() []*Route {
	routes := make([]*Route, 0)
	if t.root != nil {
		t.root.collectRoutes(&routes)
	}
	return routes
}

// collectRoutes recursively collects all routes from a node and its children
func (n *node) collectRoutes(routes *[]*Route) {
	// Add this node's route if it exists
	if n.route != nil {
		*routes = append(*routes, n.route)
	}

	// Recursively collect from children
	for _, child := range n.children {
		child.collectRoutes(routes)
	}

	// Recursively collect from param child
	if n.paramChild != nil {
		n.paramChild.collectRoutes(routes)
	}
}

// clone creates a deep copy of the tree for thread-safe copy-on-write semantics.
// Routes themselves are shared (they're immutable), but the tree structure is copied.
func (t *tree) clone() *tree {
	if t == nil {
		return nil
	}
	return &tree{
		root: t.root.clone(),
	}
}

// clone creates a deep copy of a node and all its children
// NOTE: This is kept for backward compatibility but insertWithCopy is preferred
// as it's 33-200x faster by only copying the modified path.
func (n *node) clone() *node {
	if n == nil {
		return nil
	}
	
	// Create new node with copied values
	newNode := &node{
		nType:    n.nType,
		label:    n.label,
		prefix:   n.prefix,
		paramKey: n.paramKey,
		route:    n.route, // Routes are shared (immutable)
	}
	
	// Deep copy children slice
	if len(n.children) > 0 {
		newNode.children = make([]*node, len(n.children))
		for i, child := range n.children {
			newNode.children[i] = child.clone()
		}
	} else {
		newNode.children = make([]*node, 0)
	}
	
	// Deep copy param child
	if n.paramChild != nil {
		newNode.paramChild = n.paramChild.clone()
	}
	
	return newNode
}

// insertWithCopy performs a copy-on-write insert, returning a new tree.
// Only nodes along the insertion path are copied; all other nodes are shared.
// This is significantly faster than clone+insert: ~382ns vs 12.7μs for 100-route trees.
// Thread-safe: creates new tree structure without mutating the original.
func (t *tree) insertWithCopy(path string, route *Route) *tree {
	// Normalize path
	if path == "" {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	return &tree{
		root: t.root.insertWithCopy(path, route),
	}
}

// insertWithCopy creates a copy of this node and recursively copies only the path
// that needs modification. All other children are shared (not copied).
// This implements path copying for optimal copy-on-write performance.
func (n *node) insertWithCopy(path string, route *Route) *node {
	// Create a shallow copy of this node (base structure)
	newNode := &node{
		nType:    n.nType,
		label:    n.label,
		prefix:   n.prefix,
		paramKey: n.paramKey,
		route:    n.route,
	}

	// Handle root path
	if path == "/" {
		newNode.route = route
		newNode.children = n.children       // Share children (unchanged)
		newNode.paramChild = n.paramChild   // Share param child (unchanged)
		return newNode
	}

	// Remove leading slash for processing
	path = strings.TrimPrefix(path, "/")

	// Find the next segment
	segmentEnd := strings.IndexByte(path, '/')
	var segment, remaining string

	if segmentEnd == -1 {
		// Last segment
		segment = path
		remaining = ""
	} else {
		segment = path[:segmentEnd]
		remaining = path[segmentEnd:]
	}

	// Determine node type for this segment
	var segType nodeType
	var paramKey string

	if len(segment) > 0 && segment[0] == ':' {
		segType = param
		paramKey = segment[1:] // Remove the ":"
	} else if len(segment) > 0 && segment[0] == '*' {
		segType = wildcard
		paramKey = segment[1:] // Remove the "*"
	} else {
		segType = static
	}

	// Handle parameter nodes
	if segType == param {
		newNode.children = n.children // Share static children (unchanged)

		if n.paramChild == nil {
			// Create new param child
			newNode.paramChild = &node{
				nType:    param,
				prefix:   segment,
				paramKey: paramKey,
				children: make([]*node, 0),
			}

			if remaining == "" {
				newNode.paramChild.route = route
			} else {
				newNode.paramChild = newNode.paramChild.insertWithCopy(remaining, route)
			}
		} else {
			// Recursively copy path through param child
			if remaining == "" {
				// Terminal node - copy and update route
				newNode.paramChild = &node{
					nType:      n.paramChild.nType,
					label:      n.paramChild.label,
					prefix:     n.paramChild.prefix,
					paramKey:   n.paramChild.paramKey,
					route:      route, // Updated route
					children:   n.paramChild.children,   // Share children
					paramChild: n.paramChild.paramChild, // Share param child
				}
			} else {
				newNode.paramChild = n.paramChild.insertWithCopy(remaining, route)
			}
		}
		return newNode
	}

	// Handle static nodes - look for existing child with matching prefix
	matchedIdx := -1
	var matchedChild *node
	var commonLen int

	for i, child := range n.children {
		if child.nType != static {
			continue
		}

		// Check if prefixes match
		clen := longestCommonPrefix(segment, child.prefix)

		if clen > 0 {
			matchedIdx = i
			matchedChild = child
			commonLen = clen
			break
		}
	}

	// Copy children slice (shallow - pointers are shared initially)
	newChildren := make([]*node, len(n.children))
	copy(newChildren, n.children)

	if matchedIdx >= 0 {
		// Found a matching child - need to copy this path
		if commonLen == len(matchedChild.prefix) {
			// Child prefix is a prefix of our segment
			if commonLen == len(segment) {
				// Exact match - continue down the tree
				if remaining == "" {
					// Terminal node - copy and update route
					newChildren[matchedIdx] = &node{
						nType:      matchedChild.nType,
						label:      matchedChild.label,
						prefix:     matchedChild.prefix,
						paramKey:   matchedChild.paramKey,
						route:      route, // Updated route
						children:   matchedChild.children,   // Share children
						paramChild: matchedChild.paramChild, // Share param child
					}
				} else {
					newChildren[matchedIdx] = matchedChild.insertWithCopy(remaining, route)
				}
			} else {
				// Our segment extends beyond child prefix
				newSegment := segment[commonLen:]
				newChildren[matchedIdx] = matchedChild.insertWithCopy("/"+newSegment+remaining, route)
			}
		} else {
			// Need to split the existing child (complex case)
			// Create a new split node with the common prefix
			splitNode := &node{
				nType:    static,
				label:    matchedChild.label,
				prefix:   matchedChild.prefix[:commonLen],
				children: make([]*node, 0, 2), // Will have 2 children
			}

			// Create updated child with remaining prefix
			updatedChild := &node{
				nType:      matchedChild.nType,
				label:      matchedChild.prefix[commonLen],
				prefix:     matchedChild.prefix[commonLen:],
				paramKey:   matchedChild.paramKey,
				route:      matchedChild.route,      // Keep original route
				children:   matchedChild.children,   // Share children
				paramChild: matchedChild.paramChild, // Share param child
			}
			splitNode.children = append(splitNode.children, updatedChild)

			// Now insert into the split node
			if commonLen == len(segment) {
				// Exact match with common prefix
				if remaining == "" {
					splitNode.route = route
				} else {
					splitNode = splitNode.insertWithCopy(remaining, route)
				}
			} else {
				// Need to add another child
				newSegment := segment[commonLen:]
				splitNode = splitNode.insertWithCopy("/"+newSegment+remaining, route)
			}

			newChildren[matchedIdx] = splitNode
		}
	} else {
		// No matching child - append new child
		newChild := &node{
			nType:    static,
			label:    segment[0],
			prefix:   segment,
			children: make([]*node, 0),
		}

		if remaining == "" {
			newChild.route = route
		} else {
			newChild = newChild.insertWithCopy(remaining, route)
		}

		newChildren = append(newChildren, newChild)
	}

	newNode.children = newChildren
	newNode.paramChild = n.paramChild // Share unchanged param child
	return newNode
}
