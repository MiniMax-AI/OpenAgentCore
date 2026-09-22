package codex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPreparedSessionTransfersSameResourceOnce(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resumed"}[resume], func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			if resume {
				req.AgentSessionID = "fixture-native-thread"
			}
			p, err := newPreparation(t.Context(), req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			assertPreparationOnly(t, root)
			if len(preparedCatalogs(t, root)) != 1 {
				t.Fatal("preparation did not retain its model catalog")
			}
			pid := p.session.rpc.cmd.Process.Pid
			cfgPreparedCwd := p.plan.Cwd
			// Caller-owned data cannot revise the prepared native configuration.
			req.AgentOptions["model"] = "different-model"
			req.AgentSessionID = "different-thread"
			req.WorkDir = "/different-workspace"
			copy(req.FunctionTools[0].Parameters, strings.ReplaceAll(string(req.FunctionTools[0].Parameters), "integer", "boolean"))
			out := make(chan proto.Envelope, 8)
			startCtx, stopStart := context.WithCancel(t.Context())
			started, err := p.Start(startCtx, "actual-run", proto.TextInput("actual prompt"), out)
			stopStart()
			if err != nil {
				t.Fatal(err)
			}
			session := started.(*Session)
			defer session.Cancel(context.Background())
			if session.rpc != p.session.rpc || session.rpc.cmd.Process.Pid != pid {
				t.Fatal("start replaced the prepared native resource")
			}
			if err := p.Close(); err != nil || !session.rpc.Alive() {
				t.Fatal("close cancelled transferred resource", err)
			}
			if again, err := p.Start(t.Context(), "second", proto.TextInput("second prompt"), out); err == nil || again != nil {
				t.Fatal("preparation started twice", err)
			}
			frames := waitPreparationMethod(t, root, "turn/start")
			counts := map[string]int{}
			for _, frame := range frames {
				counts[frame.Method]++
				if frame.PID != pid {
					t.Fatal("preparation and start used different children")
				}
				var params struct {
					Model        string                `json:"model"`
					ThreadID     string                `json:"threadId"`
					DynamicTools []dynamicFunctionTool `json:"dynamicTools"`
					Cwd          string                `json:"cwd"`
					Environments json.RawMessage       `json:"environments"`
				}
				if err := json.Unmarshal(frame.Params, &params); err != nil {
					t.Fatal(err)
				}
				if frame.Method == "thread/start" {
					if params.Model != "fixture-model" || len(params.DynamicTools) != 1 || !strings.Contains(string(params.DynamicTools[0].InputSchema), "integer") {
						t.Fatal("prepared configuration changed", string(frame.Params))
					}
				}
				if frame.Method == "thread/resume" && params.ThreadID != "fixture-native-thread" {
					t.Fatal("prepared resume changed")
				}
				if len(params.Environments) != 0 || (frame.Method == "thread/start" && params.Cwd != cfgPreparedCwd) || p.plan.Cwd != cfgPreparedCwd {
					t.Fatal("prepared environment changed")
				}
			}
			expectedThread := "thread/start"
			if resume {
				expectedThread = "thread/resume"
			}
			if counts["initialize"] != 1 || counts["environment/status"] != 2 || counts[expectedThread] != 1 || counts["turn/start"] != 1 {
				t.Fatal("unexpected native setup/start count", counts)
			}
			if err := session.Cancel(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-session.waitDone:
			case <-time.After(4 * time.Second):
				t.Fatal("started session did not release")
			}
			waitPreparedRelease(t, p, root)
		})
	}
}

func TestPreparedSessionAbandonmentAndFailedStart(t *testing.T) {
	for _, reason := range []string{"close", "owner cancelled", "rpc exited", "start cancelled"} {
		t.Run(reason, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			owner, cancelOwner := context.WithCancel(t.Context())
			defer cancelOwner()
			p, err := newPreparation(owner, req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			startCtx := t.Context()
			switch reason {
			case "close":
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			case "owner cancelled":
				cancelOwner()
			case "rpc exited":
				if err := p.session.rpc.Close(); err != nil {
					t.Fatal(err)
				}
			case "start cancelled":
				var cancel context.CancelFunc
				startCtx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			if reason == "owner cancelled" || reason == "rpc exited" || reason == "close" {
				// Release must happen without a later Start driving cleanup.
				waitPreparedRelease(t, p, root)
			}
			out := make(chan proto.Envelope, 8)
			if started, err := p.Start(startCtx, "late-run", proto.TextInput("must not start"), out); err == nil || started != nil {
				t.Fatal("abandoned preparation started", err)
			}
			waitPreparedRelease(t, p, root)
			assertPreparationOnly(t, root)
			if len(out) != 0 {
				t.Fatal("failed preparation emitted run output")
			}
			count := 0
			for _, frame := range preparationFrames(t, root) {
				if frame.Method == "environment/status" {
					count++
				}
			}
			if count != 2 {
				t.Fatal("failed start reconnected the native environment", count)
			}
		})
	}
}

func TestPreparedSessionConcurrentStartAndClose(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	p, err := newPreparation(t.Context(), req, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	begin := make(chan struct{})
	var wg sync.WaitGroup
	started := make(chan *Session, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			s, err := p.start(t.Context(), "run", proto.TextInput("prompt"), make(chan proto.Envelope, 8))
			if err == nil {
				started <- s
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-begin; _ = p.Close() }()
	close(begin)
	wg.Wait()
	close(started)
	count := 0
	for session := range started {
		count++
		_ = session.Cancel(context.Background())
		select {
		case <-session.waitDone:
		case <-time.After(4 * time.Second):
			t.Fatal("concurrent start retained a session")
		}
	}
	if count > 1 {
		t.Fatal("multiple ownership transfers", count)
	}
	waitPreparedRelease(t, p, root)
	turns := 0
	for _, frame := range preparationFrames(t, root) {
		if frame.Method == "turn/start" {
			turns++
		}
	}
	if turns > 1 {
		t.Fatal("multiple native Turns", turns)
	}
}

func TestPreparedSessionCancellationDuringReadiness(t *testing.T) {
	req, cfg, root := preparationFixture(t)
	t.Setenv("PARSAR_PREPARATION_BLOCK", "1")
	owner, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		p, err := newPreparation(owner, req, cfg)
		if p != nil {
			_ = p.Close()
		}
		result <- err
	}()
	waitPreparationMethod(t, root, "environment/status")
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled readiness succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled readiness did not finish")
	}
	if len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("failed readiness leaked model catalog")
	}
	assertPreparationOnly(t, root)
}
