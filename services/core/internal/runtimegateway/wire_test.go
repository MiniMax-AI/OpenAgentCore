package runtimegateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
)

// These tests run Core's gateway against a scripted Runtime that replays the
// Runtime frames of the shared wire scenarios.

const wait = 3 * time.Second

type credentialStore struct{}

func (credentialStore) GetDeviceCredential(context.Context, string) (runtimedevice.Credential, bool, error) {
	return runtimedevice.Credential{ID: prototest.DeviceID, WorkspaceID: "tenant", Type: runtimegateway.RuntimeTypeAgentDaemon, CredentialHash: runtimedevice.HashCredential(prototest.Credential)}, true, nil
}

func newGateway(t *testing.T) (*runtimegateway.Registry, string) {
	t.Helper()
	reg := runtimegateway.NewRegistry()
	handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Registry: reg, Authenticator: runtimegateway.NewAuthenticator(credentialStore{})})
	server := httptest.NewServer(http.HandlerFunc(handler.WS))
	t.Cleanup(server.Close)
	return reg, "ws" + strings.TrimPrefix(server.URL, "http")
}

// runtimePeer is the scripted Runtime end of one connection.
type runtimePeer struct {
	ws     *websocket.Conn
	frames chan proto.Envelope
}

func dialRuntime(t *testing.T, endpoint, version string) (*runtimePeer, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	query := url.Values{"device_id": {prototest.DeviceID}, "version": {version}}
	ws, response, err := websocket.DefaultDialer.DialContext(ctx, endpoint+"?"+query.Encode(), http.Header{"Authorization": {"Bearer " + prototest.Credential}})
	if err != nil {
		return nil, response, err
	}
	t.Cleanup(func() { ws.Close() })
	peer := &runtimePeer{ws: ws, frames: make(chan proto.Envelope, 16)}
	go func() {
		defer close(peer.frames)
		for {
			var env proto.Envelope
			if ws.ReadJSON(&env) != nil {
				return
			}
			peer.frames <- env
		}
	}()
	return peer, response, nil
}

func (p *runtimePeer) receive(t *testing.T) proto.Envelope {
	t.Helper()
	select {
	case env, ok := <-p.frames:
		if !ok {
			t.Fatal("Core closed the connection")
		}
		return env
	case <-time.After(wait):
		t.Fatal("no frame from Core")
	}
	return proto.Envelope{}
}

func (p *runtimePeer) silent(t *testing.T) {
	t.Helper()
	select {
	case env, ok := <-p.frames:
		if !ok {
			t.Fatal("Core closed the connection")
		}
		t.Fatalf("Core sent %s %q; reconnect must not replay work", env.Type, env.ID)
	case <-time.After(prototest.SilenceWindow):
	}
}

type receipt struct {
	ack proto.InteractionDecisionAckPayload
	err error
}

// coreSide drives the gateway the way Core's execution layer does.
type coreSide struct {
	t            *testing.T
	reg          *runtimegateway.Registry
	endpoint     string
	session      *runtimegateway.Session
	peer         *runtimePeer
	preparations map[string]*runtimegateway.Subscription
	runs         map[string]*runtimegateway.Subscription
	receipts     map[string]chan receipt
}

func (c *coreSide) connect() {
	c.t.Helper()
	peer, _, err := dialRuntime(c.t, c.endpoint, proto.Version)
	if err != nil {
		c.t.Fatal(err)
	}
	session, err := c.reg.WaitForDevice(c.t.Context(), prototest.DeviceID, wait)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { session.Close("test finished") })
	c.peer, c.session = peer, session
}

func TestWireRejectsIncompatibleVersions(t *testing.T) {
	reg, endpoint := newGateway(t)
	for _, version := range prototest.IncompatibleVersions() {
		t.Run(version, func(t *testing.T) {
			_, response, err := dialRuntime(t, endpoint, version)
			if err == nil || response == nil || response.StatusCode != prototest.IncompatibleVersionStatus {
				t.Fatalf("upgrade with version %q: %v", version, err)
			}
			var body struct {
				Error string `json:"error"`
			}
			if json.NewDecoder(response.Body).Decode(&body) != nil || body.Error != prototest.IncompatibleVersionCode {
				t.Fatalf("rejection code %q", body.Error)
			}
			if devices := reg.Devices(); len(devices) != 0 {
				t.Fatalf("rejected connection registered %v", devices)
			}
		})
	}
}

func TestWireScenarios(t *testing.T) {
	for _, scenario := range prototest.WireScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			reg, endpoint := newGateway(t)
			c := &coreSide{t: t, reg: reg, endpoint: endpoint, preparations: map[string]*runtimegateway.Subscription{}, runs: map[string]*runtimegateway.Subscription{}, receipts: map[string]chan receipt{}}
			c.connect()
			// Subscribing before any frame also observes that a failed
			// preparation produces no Run result.
			run, err := c.session.SubscribeDurable(prototest.RunID)
			if err != nil {
				t.Fatal(err)
			}
			c.runs[prototest.RunID] = run
			for _, step := range scenario.Steps {
				switch step.Action {
				case prototest.Send:
					if step.From == prototest.Core {
						c.coreSends(step.Frame)
					} else {
						c.runtimeSends(step.Frame)
					}
				case prototest.Silence:
					if step.From == prototest.Core {
						c.peer.silent(t)
					} else {
						c.noRunOutput()
					}
				case prototest.Settle:
					c.receiptsPending()
				case prototest.Disconnect:
					c.disconnect()
				case prototest.Reconnect:
					c.reconnect()
				}
			}
		})
	}
}

// coreSends sends a Core frame the way execution does and checks that the
// Runtime receives it unchanged.
func (c *coreSide) coreSends(frame proto.Envelope) {
	t := c.t
	t.Helper()
	switch frame.Type {
	case proto.TypeExecutionPrepare:
		sub, err := c.session.SubscribePreparation(frame.ID)
		if err != nil {
			t.Fatal(err)
		}
		c.preparations[frame.ID] = sub
		c.send(frame)
	case proto.TypePromptCancel:
		var request proto.PromptCancelPayload
		if err := frame.DecodePayload(&request); err != nil {
			t.Fatal(err)
		}
		done := make(chan receipt, 1)
		c.receipts[request.DeliveryID] = done
		go func() {
			ack, err := c.session.SendAndWaitInteractionAck(t.Context(), frame, request.DeliveryID)
			done <- receipt{ack, err}
		}()
	default:
		c.send(frame)
	}
	if err := prototest.SameFrame(frame, c.peer.receive(t)); err != nil {
		t.Fatal(err)
	}
}

func (c *coreSide) send(frame proto.Envelope) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(c.t.Context(), wait)
	defer cancel()
	if err := c.session.Send(ctx, frame); err != nil {
		c.t.Fatal(err)
	}
}

// runtimeSends replays a Runtime frame and checks where Core delivers it.
func (c *coreSide) runtimeSends(frame proto.Envelope) {
	t := c.t
	t.Helper()
	switch frame.Type {
	case proto.TypeInteractionDecisionAck:
		var want proto.InteractionDecisionAckPayload
		if err := frame.DecodePayload(&want); err != nil {
			t.Fatal(err)
		}
		done := c.receipts[want.DeliveryID]
		if done == nil {
			t.Fatalf("no pending receipt for %q", want.DeliveryID)
		}
		select {
		case got := <-done:
			t.Fatalf("receipt returned before the Runtime sent it: %+v, %v", got.ack, got.err)
		default:
		}
		c.write(frame)
		select {
		case got := <-done:
			if got.err != nil || !reflect.DeepEqual(got.ack, want) {
				t.Fatalf("receipt %+v, %v; want %+v", got.ack, got.err, want)
			}
		case <-time.After(wait):
			t.Fatal("missing receipt")
		}
		delete(c.receipts, want.DeliveryID)
	case proto.TypePreparationStatus:
		c.write(frame)
		c.delivered(c.preparations[frame.ID], frame)
	default:
		c.write(frame)
		c.delivered(c.runs[frame.ID], frame)
	}
}

func (c *coreSide) write(frame proto.Envelope) {
	c.t.Helper()
	if err := c.peer.ws.WriteJSON(frame); err != nil {
		c.t.Fatal(err)
	}
}

func (c *coreSide) delivered(sub *runtimegateway.Subscription, frame proto.Envelope) {
	t := c.t
	t.Helper()
	if sub == nil {
		t.Fatalf("no subscription for %s %q", frame.Type, frame.ID)
	}
	select {
	case env, ok := <-sub.Events:
		if !ok {
			t.Fatalf("subscription closed: %v", sub.Err())
		}
		if err := prototest.SameFrame(frame, env); err != nil {
			t.Fatal(err)
		}
	case <-time.After(wait):
		t.Fatalf("%s %q was not delivered", frame.Type, frame.ID)
	}
}

func (c *coreSide) noRunOutput() {
	c.t.Helper()
	timeout := time.After(prototest.SilenceWindow)
	for runID, sub := range c.runs {
		select {
		case env, ok := <-sub.Events:
			c.t.Fatalf("Run %q received %s (open=%t) without Runtime output", runID, env.Type, ok)
		case <-timeout:
		}
	}
}

func (c *coreSide) receiptsPending() {
	c.t.Helper()
	timeout := time.After(prototest.SilenceWindow)
	for id, done := range c.receipts {
		select {
		case got := <-done:
			c.t.Fatalf("receipt %q preceded native settlement: %+v, %v", id, got.ack, got.err)
		case <-timeout:
		}
	}
}

func (c *coreSide) disconnect() {
	t := c.t
	t.Helper()
	c.peer.ws.Close()
	select {
	case <-c.session.Closed():
	case <-time.After(wait):
		t.Fatal("gateway stayed connected")
	}
	for runID, sub := range c.runs {
		for env := range sub.Events {
			if env.Type == proto.TypeError || env.Type == proto.TypeDone {
				t.Fatalf("disconnect manufactured %s for Run %q", env.Type, runID)
			}
		}
		if !errors.Is(sub.Err(), runtimegateway.ErrSessionClosed) {
			t.Fatalf("Run %q disconnect outcome: %v", runID, sub.Err())
		}
	}
	for deadline := time.Now().Add(wait); ; time.Sleep(time.Millisecond) {
		if _, err := c.reg.LookupDevice(prototest.DeviceID); errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lost connection stayed registered")
		}
	}
}

func (c *coreSide) reconnect() {
	t := c.t
	t.Helper()
	previous := c.session
	c.connect()
	if c.session == previous {
		t.Fatal("reconnect reused the lost connection")
	}
	for runID := range c.runs {
		if c.reg.LookupRun(runID) != nil {
			t.Fatalf("reconnect inherited Run %q", runID)
		}
	}
}
