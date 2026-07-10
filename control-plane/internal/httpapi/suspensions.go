package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
)

const (
	suspensionAckTimeout = 15 * time.Second
	suspensionLease      = 45 * time.Second
)

type suspensionDelivery struct {
	id    string
	event control.SuspensionEvent
	ack   chan error
}

type suspensionGateway struct {
	events   chan suspensionDelivery
	done     chan struct{}
	lastSeen time.Time
}

type pendingSuspensionDelivery struct {
	gatewayID string
	events    chan suspensionDelivery
	done      <-chan struct{}
	delivery  suspensionDelivery
}

// suspensionHub fans each store event out to every registered standalone
// gateway and does not acknowledge the store mutation until all gateways ack.
type suspensionHub struct {
	mu       sync.Mutex
	gateways map[string]*suspensionGateway
	pending  map[string]pendingSuspensionDelivery
	seq      atomic.Uint64
}

func newSuspensionHub() *suspensionHub {
	return &suspensionHub{
		gateways: make(map[string]*suspensionGateway),
		pending:  make(map[string]pendingSuspensionDelivery),
	}
}

func (h *suspensionHub) register(id string) error {
	if id == "" {
		return fmt.Errorf("gatewayID is required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.gateways[id]; !ok {
		h.gateways[id] = &suspensionGateway{
			events: make(chan suspensionDelivery, 8), done: make(chan struct{}),
		}
	}
	h.gateways[id].lastSeen = time.Now()
	return nil
}

func (h *suspensionHub) unregister(id string) {
	h.mu.Lock()
	h.unregisterLocked(id)
	h.mu.Unlock()
}

func (h *suspensionHub) unregisterLocked(id string) {
	if gw := h.gateways[id]; gw != nil {
		delete(h.gateways, id)
		close(gw.done)
	}
	for deliveryID, pending := range h.pending {
		if pending.gatewayID == id {
			select {
			case pending.delivery.ack <- fmt.Errorf("gateway %q disconnected before acknowledgement", id):
			default:
			}
			delete(h.pending, deliveryID)
		}
	}
}

func (h *suspensionHub) publish(event control.SuspensionEvent) error {
	h.mu.Lock()
	deliveries := make([]suspensionDelivery, 0, len(h.gateways))
	for gatewayID, gw := range h.gateways {
		if time.Since(gw.lastSeen) > suspensionLease {
			h.unregisterLocked(gatewayID)
			continue
		}
		delivery := suspensionDelivery{
			id: fmt.Sprintf("kill-%d", h.seq.Add(1)), event: event, ack: make(chan error, 1),
		}
		h.pending[delivery.id] = pendingSuspensionDelivery{
			gatewayID: gatewayID, events: gw.events, done: gw.done, delivery: delivery,
		}
		deliveries = append(deliveries, delivery)
	}
	h.mu.Unlock()
	// Do not hold h.mu while waiting for a gateway's bounded event queue.  A
	// full queue is drained by next/ack requests, which also need h.mu; holding
	// it here would deadlock a busy gateway and leave its cancellation
	// unacknowledged indefinitely.
	for _, delivery := range deliveries {
		h.mu.Lock()
		pending, ok := h.pending[delivery.id]
		h.mu.Unlock()
		if !ok {
			// The gateway disconnected between snapshotting it and dispatching
			// the event. unregister has already reported that delivery's error.
			continue
		}
		// The channel belongs to the original gateway instance, even if an
		// identically named gateway has since re-registered.
		select {
		case pending.events <- delivery:
		case <-pending.done:
		}
	}
	defer func() {
		h.mu.Lock()
		for _, delivery := range deliveries {
			delete(h.pending, delivery.id)
		}
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), suspensionAckTimeout)
	defer cancel()
	for _, delivery := range deliveries {
		select {
		case err := <-delivery.ack:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for gateway cancellation acknowledgement: %w", ctx.Err())
		}
	}
	return nil
}

func (h *suspensionHub) next(ctx context.Context, gatewayID string) (suspensionDelivery, bool, error) {
	h.mu.Lock()
	gw := h.gateways[gatewayID]
	if gw != nil {
		gw.lastSeen = time.Now()
	}
	h.mu.Unlock()
	if gw == nil {
		return suspensionDelivery{}, false, fmt.Errorf("gateway %q is not registered", gatewayID)
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case delivery := <-gw.events:
		return delivery, true, nil
	case <-timer.C:
		return suspensionDelivery{}, false, nil
	case <-ctx.Done():
		return suspensionDelivery{}, false, ctx.Err()
	}
}

func (h *suspensionHub) ack(gatewayID, deliveryID string) error {
	h.mu.Lock()
	pending, ok := h.pending[deliveryID]
	if ok && pending.gatewayID == gatewayID {
		delete(h.pending, deliveryID)
		if gw := h.gateways[gatewayID]; gw != nil {
			gw.lastSeen = time.Now()
		}
	}
	h.mu.Unlock()
	if !ok || pending.gatewayID != gatewayID {
		return fmt.Errorf("unknown suspension delivery %q", deliveryID)
	}
	pending.delivery.ack <- nil
	return nil
}

func (s *Server) handleGatewaySuspensions(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.RegisterSuspensionsRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	switch r.Method {
	case http.MethodPost:
		if err := s.suspensions.register(req.GatewayID); err != nil {
			writeGatewayError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		s.suspensions.unregister(req.GatewayID)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGatewaySuspensionNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.NextSuspensionRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	delivery, ok, err := s.suspensions.next(r.Context(), req.GatewayID)
	if err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, gatewayapi.SuspensionResponse{
		DeliveryID: delivery.id, Scope: string(delivery.event.Scope), ID: delivery.event.ID,
	})
}

func (s *Server) handleGatewaySuspensionAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.AckSuspensionRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if err := s.suspensions.ack(req.GatewayID, req.DeliveryID); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
