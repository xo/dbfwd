package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/xo/dbfwd"
)

// reconnectWait is how long a forward tries to reconnect before it reports
// that it is lost (D16).
const reconnectWait = 2 * time.Minute

// managed is a forward that watches its pod and reconnects on the same local
// port when the pod goes, stops being ready, or stops matching its role
// (D16).
type managed struct {
	r     runner
	k     *kube
	p     *picker
	local int
	wait  time.Duration

	// cancel stops the context that the forward reconnects with.
	cancel context.CancelFunc

	stopping chan struct{}
	done     chan struct{}
	once     sync.Once

	mu  sync.Mutex
	err error
	t   tunnel
}

// startManaged picks a pod, starts the forward, and returns once it is ready.
// Canceling ctx stops only the start. The forward reconnects with a context
// that keeps the values of ctx but not its deadline or its cancel, because the
// forward outlives the call, and Close cancels it.
func startManaged(ctx context.Context, k *kube, p *picker, wait time.Duration) (*managed, error) {
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m := &managed{r: k.r, k: k, p: p, wait: wait, cancel: cancel, stopping: make(chan struct{}), done: make(chan struct{})}
	pk, err := pickWithin(ctx, p, podWait)
	if err != nil {
		cancel()
		return nil, err
	}
	t, err := m.forward(ctx, pk)
	if err != nil {
		cancel()
		return nil, err
	}
	m.t, m.local = t, t.port()
	go m.run(rctx, pk)
	return m, nil
}

// Done returns a channel that is closed when the forward stops for good.
func (m *managed) Done() <-chan struct{} { return m.done }

// Err returns why the forward stopped, after Done is closed.
func (m *managed) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return fmt.Errorf("%w: %w", dbfwd.ErrForwardLost, m.err)
	}
	return nil
}

// Close stops the forward and waits for it.
func (m *managed) Close() error {
	m.once.Do(func() {
		close(m.stopping)
		m.cancel()
	})
	<-m.done
	m.mu.Lock()
	t := m.t
	m.mu.Unlock()
	return t.Close()
}

// forward starts kubectl port-forward to a pod, on the local port of the
// forward once it has one.
func (m *managed) forward(ctx context.Context, pk picked) (tunnel, error) {
	ports := ":" + strconv.Itoa(pk.port)
	if m.local != 0 {
		ports = strconv.Itoa(m.local) + ports
	}
	return m.r.forward(ctx, m.k.args("port-forward", "--address", "127.0.0.1", "--pod-running-timeout", podWait.String(), "pod/"+pk.pod, ports)...)
}

// run watches the pod, and reconnects when it goes.
func (m *managed) run(ctx context.Context, pk picked) {
	defer close(m.done)
	for {
		if !m.follow(pk) {
			return
		}
		next, ok := m.reconnect(ctx)
		if !ok {
			return
		}
		pk = next
	}
}

// follow watches the pod of the forward until it must reconnect, and returns
// false when Close stopped it.
func (m *managed) follow(pk picked) bool {
	m.mu.Lock()
	t := m.t
	m.mu.Unlock()
	w, err := m.r.watch(m.k.args("get", "pod", pk.pod, "--watch", "--output-watch-events", "--output", "json")...)
	if err != nil {
		// with no watch, the forward still reconnects when kubectl stops
		w = nil
	}
	var events <-chan event
	if w != nil {
		events = w.events()
		defer w.stop()
	}
	for {
		select {
		case <-m.stopping:
			return false
		case <-t.Done():
			return true
		case e, ok := <-events:
			if !ok {
				// the watch ended, so it starts again
				return m.follow(pk)
			}
			if e.Type == "DELETED" || !running(e.Object) || (pk.ready && !ready(e.Object)) || (pk.selector != nil && !pk.selector.matches(e.Object.labels())) {
				_ = t.Close()
				return true
			}
		}
	}
}

// reconnect picks a pod again and starts the forward on the same local port.
// It returns false when it gives up, or when Close stopped it.
func (m *managed) reconnect(ctx context.Context) (picked, bool) {
	deadline := time.Now().Add(m.wait)
	delay := 500 * time.Millisecond
	var last error
	for time.Now().Before(deadline) {
		tctx, cancel := context.WithTimeout(ctx, time.Minute)
		pk, err := m.p.pick(tctx)
		if err == nil {
			var t tunnel
			if t, err = m.forward(tctx, pk); err == nil {
				cancel()
				m.mu.Lock()
				m.t = t
				m.mu.Unlock()
				return pk, true
			}
		}
		cancel()
		last = err
		select {
		case <-m.stopping:
			return picked{}, false
		case <-time.After(delay):
		}
		delay = min(2*delay, 5*time.Second)
	}
	m.mu.Lock()
	m.err = fmt.Errorf("reconnecting for %s: %w", m.wait, last)
	m.mu.Unlock()
	return picked{}, false
}

// port returns the local port, which stays the same across reconnects.
func (m *managed) port() int { return m.local }

// pickWithin picks a pod, and waits up to d for one to run, as kubectl
// port-forward does. The lookup already found the target, so a pod that is
// not found yet is one that an operator is replacing.
func pickWithin(ctx context.Context, p *picker, d time.Duration) (picked, error) {
	deadline := time.Now().Add(d)
	for {
		pk, err := p.pick(ctx)
		if err == nil || !errors.Is(err, dbfwd.ErrNotFound) || time.Now().After(deadline) {
			return pk, err
		}
		select {
		case <-ctx.Done():
			return picked{}, fmt.Errorf("waiting for a pod: %w", context.Cause(ctx))
		case <-time.After(time.Second):
		}
	}
}
