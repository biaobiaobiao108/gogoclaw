package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/readline"

	"github.com/biaobiaobiao108/gogoclaw/cmd/gogoclaw/internal"
	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/approval"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/instance"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func agentCmd(message, sessionKey, model string, debug, resume bool) error {
	dataDirLock, err := instance.AcquireDataDirLock(internal.GetGogoclawHome())
	if err != nil {
		return fmt.Errorf("another GoGoClaw runtime may already be using this data directory: %w", err)
	}
	defer func() {
		if err := dataDirLock.Release(); err != nil {
			logger.Warnf("release data directory lock failed: %v", err)
		}
	}()

	if sessionKey == "" {
		sessionKey = "cli:default"
	}

	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	logger.ConfigureFromEnv()

	if debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Println("🔍 Debug mode enabled")
	}

	if model != "" {
		cfg.Agents.Defaults.ModelName = model
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	agentLoop := agent.NewAgentLoop(cfg, msgBus, provider)
	defer agentLoop.Close()

	// Print agent startup info (only for interactive mode)
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo, ok := startupInfo["tools"].(map[string]any)
	if !ok {
		toolsInfo = nil
	}
	skillsInfo, ok := startupInfo["skills"].(map[string]any)
	if !ok {
		skillsInfo = nil
	}
	logFields := map[string]any{}
	if toolsInfo != nil {
		logFields["tools_count"] = toolsInfo["count"]
	}
	if skillsInfo != nil {
		logFields["skills_total"] = skillsInfo["total"]
		logFields["skills_available"] = skillsInfo["available"]
	}
	logger.InfoCF("agent", "Agent initialized", logFields)

	if message != "" {
		ctx := context.Background()
		response, err := processDirectWithActivity(ctx, agentLoop, message, sessionKey)
		if err != nil {
			return fmt.Errorf("error processing message: %w", err)
		}
		fmt.Printf("\n%s %s\n", internal.Logo, response)
		return nil
	}

	fmt.Printf("%s Interactive mode (Ctrl+C to exit)\n\n", internal.Logo)
	configureCLIApproval(agentLoop)
	if resume {
		ctx := context.Background()
		response, err := processResumeWithActivity(ctx, agentLoop, sessionKey)
		if err != nil {
			fmt.Printf("Resume failed: %v\n", err)
		} else {
			fmt.Printf("\n%s %s\n\n", internal.Logo, response)
		}
	}
	interactiveMode(agentLoop, sessionKey)

	return nil
}

func interactiveMode(agentLoop *agent.AgentLoop, sessionKey string) {
	prompt := fmt.Sprintf("%s You: ", internal.Logo)

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     filepath.Join(os.TempDir(), ".gogoclaw_history"),
		HistoryLimit:    100,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Error initializing readline: %v\n", err)
		fmt.Println("Falling back to simple input mode...")
		simpleInteractiveMode(agentLoop, sessionKey)
		return
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt || err == io.EOF {
				fmt.Println("\nGoodbye!")
				return
			}
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			return
		}
		if input == "/resume" {
			ctx := context.Background()
			response, err := processResumeWithActivity(ctx, agentLoop, sessionKey)
			if err != nil {
				fmt.Printf("Resume failed: %v\n", err)
				continue
			}
			fmt.Printf("\n%s %s\n\n", internal.Logo, response)
			continue
		}

		ctx := context.Background()
		response, err := processDirectWithActivity(ctx, agentLoop, input, sessionKey)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("\n%s %s\n\n", internal.Logo, response)
	}
}

func simpleInteractiveMode(agentLoop *agent.AgentLoop, sessionKey string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(fmt.Sprintf("%s You: ", internal.Logo))
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println("\nGoodbye!")
				return
			}
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			return
		}
		if input == "/resume" {
			ctx := context.Background()
			response, err := processResumeWithActivity(ctx, agentLoop, sessionKey)
			if err != nil {
				fmt.Printf("Resume failed: %v\n", err)
				continue
			}
			fmt.Printf("\n%s %s\n\n", internal.Logo, response)
			continue
		}

		ctx := context.Background()
		response, err := processDirectWithActivity(ctx, agentLoop, input, sessionKey)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("\n%s %s\n\n", internal.Logo, response)
	}
}

func processDirectWithActivity(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	message, sessionKey string,
) (string, error) {
	return processWithActivity(ctx, agentLoop, sessionKey, func(ctx context.Context) (string, error) {
		return agentLoop.ProcessDirect(ctx, message, sessionKey)
	})
}

func processResumeWithActivity(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	sessionKey string,
) (string, error) {
	return processWithActivity(ctx, agentLoop, sessionKey, func(ctx context.Context) (string, error) {
		return agentLoop.ResumeSession(ctx, sessionKey)
	})
}

func processWithActivity(
	ctx context.Context,
	agentLoop *agent.AgentLoop,
	sessionKey string,
	run func(context.Context) (string, error),
) (string, error) {
	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()

	var wg sync.WaitGroup
	if events := agentLoop.RuntimeEvents(); events != nil {
		sub, ch, err := events.OfKind(
			runtimeevents.KindAgentToolExecStart,
			runtimeevents.KindAgentToolExecEnd,
			runtimeevents.KindAgentContextCompress,
			runtimeevents.KindAgentLLMRetry,
		).SubscribeChan(eventCtx, runtimeevents.SubscribeOptions{Name: "cli-activity", Buffer: 32})
		if err == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer sub.Close()
				for {
					select {
					case <-eventCtx.Done():
						return
					case evt, ok := <-ch:
						if !ok {
							return
						}
						printActivityEvent(os.Stdout, evt)
					}
				}
			}()
		}
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	interrupts := make(chan os.Signal, 2)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	stopSignals := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			fmt.Print("\nStopping task... (Ctrl+C again to force)\n")
			if err := agentLoop.HardAbort(sessionKey); err != nil {
				fmt.Printf("Stop failed: %v\n", err)
			}
		case <-stopSignals:
			return
		}
		select {
		case <-interrupts:
			fmt.Print("\nForce stopping task...\n")
			cancelRun()
		case <-stopSignals:
		case <-runCtx.Done():
		}
	}()

	response, err := run(runCtx)
	close(stopSignals)
	cancelEvents()
	wg.Wait()
	return response, err
}

func configureCLIApproval(agentLoop *agent.AgentLoop) {
	configureCLIApprovalWithIO(agentLoop, os.Stdin, os.Stdout)
}

func configureCLIApprovalWithIO(agentLoop *agent.AgentLoop, input io.Reader, output io.Writer) {
	if agentLoop == nil {
		return
	}
	reader := bufio.NewReader(input)
	agentLoop.SetApprovalNotifier(func(ctx context.Context, req approval.Request) error {
		args := strings.TrimSpace(strings.ReplaceAll(
			fmt.Sprintf("%v", req.Arguments),
			"\n",
			" ",
		))
		if len(args) > 300 {
			args = args[:300] + "..."
		}
		fmt.Fprintf(output, "\nApproval required: %s\n", req.Tool)
		if args != "" {
			fmt.Fprintf(output, "Arguments: %s\n", args)
		}
		if req.RiskSummary != "" {
			fmt.Fprintf(output, "Risk: %s\n", req.RiskSummary)
		}
		fmt.Fprint(output, "Approve? [o]nce / [s]ession / [d]eny: ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		response := cliApprovalResponse(req.ID, line)
		if err := agentLoop.RespondApproval(ctx, response); err != nil {
			return err
		}
		return nil
	})
}

func cliApprovalResponse(requestID, answer string) approval.Response {
	answer = strings.ToLower(strings.TrimSpace(answer))
	response := approval.Response{
		ID:      requestID,
		Approve: answer == "o" || answer == "once" || answer == "s" || answer == "session",
		Scope:   approval.ScopeOnce,
		Reason:  "denied from CLI",
	}
	if answer == "s" || answer == "session" {
		response.Scope = approval.ScopeSession
		response.Reason = ""
	} else if response.Approve {
		response.Reason = ""
	}
	return response
}

func printActivityEvent(w io.Writer, evt runtimeevents.Event) {
	switch payload := evt.Payload.(type) {
	case agent.ToolExecStartPayload:
		fmt.Fprintf(w, "\r\x1b[2K  → %s\n", payload.Tool)
	case agent.ToolExecEndPayload:
		status := "✓"
		if payload.IsError {
			status = "×"
		}
		fmt.Fprintf(w, "  %s %s · %s\n", status, payload.Tool, payload.Duration.Round(time.Millisecond))
	case agent.ContextCompressPayload:
		fmt.Fprintf(w, "  ↻ context compacted · %d messages kept\n", payload.RemainingMessages)
	case agent.LLMRetryPayload:
		fmt.Fprintf(w, "  ↻ retrying model · %s\n", payload.Reason)
	}
}
