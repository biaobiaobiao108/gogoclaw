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

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func agentCmd(message, sessionKey, model string, debug bool) error {
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

	provider, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	// Use the resolved model ID from provider creation
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
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
	interactiveMode(agentLoop, sessionKey)

	return nil
}

func interactiveMode(agentLoop *agent.AgentLoop, sessionKey string) {
	prompt := fmt.Sprintf("%s You: ", internal.Logo)

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     filepath.Join(os.TempDir(), ".picoclaw_history"),
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
						printActivityEvent(evt)
					}
				}
			}()
		}
	}

	interrupts := make(chan os.Signal, 2)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	stopSignals := make(chan struct{})
	go func() {
		select {
		case <-interrupts:
			fmt.Print("\nStopping task… (Ctrl+C again to force)\n")
			if err := agentLoop.HardAbort(sessionKey); err != nil {
				fmt.Printf("Stop failed: %v\n", err)
			}
		case <-stopSignals:
		}
		select {
		case <-interrupts:
		case <-stopSignals:
		}
	}()

	response, err := agentLoop.ProcessDirect(ctx, message, sessionKey)
	close(stopSignals)
	cancelEvents()
	wg.Wait()
	return response, err
}

func printActivityEvent(evt runtimeevents.Event) {
	switch payload := evt.Payload.(type) {
	case agent.ToolExecStartPayload:
		fmt.Printf("\r\x1b[2K  → %s\n", payload.Tool)
	case agent.ToolExecEndPayload:
		status := "✓"
		if payload.IsError {
			status = "×"
		}
		fmt.Printf("  %s %s · %s\n", status, payload.Tool, payload.Duration.Round(time.Millisecond))
	case agent.ContextCompressPayload:
		fmt.Printf("  ↻ context compacted · %d messages kept\n", payload.RemainingMessages)
	case agent.LLMRetryPayload:
		fmt.Printf("  ↻ retrying model · %s\n", payload.Reason)
	}
}
