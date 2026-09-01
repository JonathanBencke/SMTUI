package mcp

import (
	"sync"
	"time"
)

// notifyDebounce is the minimum interval between two resources/updated
// notifications for the same URI. Verbose stacks (Maven, WildFly) emit log
// lines in bursts of hundreds per second; without coalescing, subscribers would
// be flooded with notifications for output they have not read yet.
const notifyDebounce = time.Second

// notifier turns service events into MCP resource notifications, coalescing
// bursts per URI.
type notifier struct {
	srv      *MCPServer
	debounce time.Duration

	mu      sync.Mutex
	pending map[string]*time.Timer
	stopped bool
}

func newNotifier(srv *MCPServer) *notifier {
	return &notifier{
		srv:      srv,
		debounce: notifyDebounce,
		pending:  map[string]*time.Timer{},
	}
}

// serviceLog invalidates the log resource of a service.
func (n *notifier) serviceLog(name string) {
	n.schedule(serviceURI(name) + "/logs")
}

// serviceStatus invalidates both the service resource and the aggregated
// service list, since a status change shows up in both.
func (n *notifier) serviceStatus(name string) {
	n.schedule(serviceURI(name))
	n.schedule(uriServices)
}

// schedule queues a notification for uri, ignoring the call when one is already
// pending for that URI.
func (n *notifier) schedule(uri string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.stopped {
		return
	}
	if _, exists := n.pending[uri]; exists {
		return
	}
	n.pending[uri] = time.AfterFunc(n.debounce, func() { n.fire(uri) })
}

func (n *notifier) fire(uri string) {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	delete(n.pending, uri)
	n.mu.Unlock()

	n.srv.srv.SendNotificationToAllClients("notifications/resources/updated", map[string]any{
		"uri": uri,
	})
}

// stop cancels every pending notification and stops accepting new ones. The
// TUI can toggle the MCP server off and on again, so start() reopens it.
func (n *notifier) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.stopped = true
	for uri, timer := range n.pending {
		timer.Stop()
		delete(n.pending, uri)
	}
}

// start re-enables notifications after a stop.
func (n *notifier) start() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopped = false
}
