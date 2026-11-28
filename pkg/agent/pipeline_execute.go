// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/constants"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
	"github.com/biaobiaobiao108/gogoclaw/pkg/utils"
)

// ExecuteTools executes the tool loop, handling BeforeTool/ApproveTool/AfterTool hooks,
// tool execution with async callbacks, media delivery, and steering injection.
// Returns ToolControl indicating what the coordinator should do next:
//   - ToolControlContinue: all tool results handled, pendingMessages or steering exists, continue turn
//   - ToolControlBreak: tool loop exited, proceed to coordinator's hardAbort/finalContent/finalize
func (p *Pipeline) ExecuteTools(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	iteration int,
) ToolControl {
	al := p.al
	cfg := p.Cfg
	mediaStore := p.MediaStore
	channelManager := p.ChannelManager
	normalizedToolCalls := exec.normalizedToolCalls

	messages := exec.messages
	handledAttachments := make([]providers.Attachment, 0)
	toolBatchID := ts.beginToolBatch(iteration, normalizedToolCalls)

	var preExecutedResults []*tools.ToolResult
	var parallelActivities []string
	var parallelDurations []time.Duration
	if len(normalizedToolCalls) > 1 && canExecuteInParallel(al, cfg, ts, normalizedToolCalls) {
		var ok bool
		preExecutedResults, parallelActivities, parallelDurations, ok = p.executeParallelToolCalls(turnCtx, ts, exec, toolBatchID, normalizedToolCalls)
		if !ok {
			return ToolControlBreak
		}
	}

toolLoop:
	for i, tc := range normalizedToolCalls {
		if ts.hardAbortRequested() {
			ts.markToolItemTerminal(toolBatchID, i, toolItemStatusCancelled, toolItemSideEffectNone, "Tool cancelled before execution.")
			exec.abortedByHardAbort = true
			return ToolControlBreak
		}

		toolName := tc.Name
		toolArgs := cloneStringAnyMap(tc.Arguments)
		denyByTurnProfile := func() bool {
			if turnProfileToolAllowed(ts.profile, toolName) {
				return false
			}
			exec.allResponsesHandled = false
			denyContent := fmt.Sprintf("Tool %q is not allowed by the active turn profile.", toolName)
			ts.markToolItemTerminal(toolBatchID, i, toolItemStatusDenied, toolItemSideEffectNone, denyContent)
			al.emitEvent(
				runtimeevents.KindAgentToolExecSkipped,
				ts.eventMeta("runTurn", "turn.tool.skipped"),
				ToolExecSkippedPayload{
					CallID: tc.ID,
					Tool:   toolName,
					Reason: denyContent,
				},
			)
			deniedMsg := providers.Message{
				Role:       "tool",
				Content:    denyContent,
				ToolCallID: tc.ID,
			}
			messages = append(messages, deniedMsg)
			if !ts.opts.NoHistory {
				if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, deniedMsg); err != nil {
					exec.executionErr = fmt.Errorf("write denied tool result: %w", err)
					return true
				}
				ts.recordPersistedMessage(deniedMsg)
			}
			return true
		}

		if denyByTurnProfile() {
			if exec.executionErr != nil {
				return ToolControlBreak
			}
			continue
		}

		if al.hooks != nil {
			toolReq, decision := al.hooks.BeforeTool(turnCtx, &ToolCallHookRequest{
				Meta:      ts.eventMeta("runTurn", "turn.tool.before"),
				Context:   cloneTurnContext(ts.turnCtx),
				Tool:      toolName,
				Arguments: toolArgs,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if toolReq != nil {
					toolName = toolReq.Tool
					toolArgs = toolReq.Arguments
				}
			case HookActionRespond:
				if toolReq != nil && toolReq.HookResult != nil {
					hookResult := toolReq.HookResult
					toolPolicy := ts.toolPolicy(toolName)
					ts.markToolItemRunning(toolBatchID, i, toolName, toolArgs)

					logger.InfoCF("agent", "Tool call handled by hook",
						map[string]any{
							"turn_id":   ts.turnID,
							"agent_id":  ts.agent.ID,
							"tool":      toolName,
							"iteration": iteration,
						})

					al.emitEvent(
						runtimeevents.KindAgentToolExecStart,
						ts.eventMeta("runTurn", "turn.tool.start"),
						ToolExecStartPayload{
							CallID:    tc.ID,
							Tool:      toolName,
							Arguments: cloneEventArguments(toolArgs),
						},
					)

					if shouldPublishToolFeedback(cfg, ts) {
						toolFeedbackMaxLen := cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength()
						toolFeedbackExplanation := exec.toolFeedbackExplanation(tc, messages)
						feedbackMsg := utils.FormatToolFeedbackMessage(
							toolName,
							toolFeedbackExplanation,
							toolFeedbackArgsPreview(toolArgs, toolFeedbackMaxLen),
						)
						fbCtx, fbCancel := context.WithTimeout(turnCtx, 3*time.Second)
						_ = al.bus.PublishOutbound(fbCtx, outboundMessageForTurnWithOptions(
							ts,
							feedbackMsg,
							outboundTurnMessageOptions{kind: messageKindToolFeedback},
						))
						fbCancel()
					}

					toolDuration := time.Duration(0)

					shouldSendForUser := !hookResult.Silent && hookResult.ForUser != "" &&
						(ts.opts.SendResponse || hookResult.ResponseHandled)
					if shouldSendForUser {
						al.bus.PublishOutbound(ctx, bus.OutboundMessage{
							Context: bus.InboundContext{
								Channel: ts.channel,
								ChatID:  ts.chatID,
								Raw: map[string]string{
									"is_tool_call": "true",
								},
							},
							Content: cfg.RedactDiagnostic(hookResult.ForUser),
						})
					}

					al.pinTurnMedia(ts, hookResult.Media)
					if len(hookResult.Media) > 0 && hookResult.ResponseHandled {
						parts := make([]bus.MediaPart, 0, len(hookResult.Media))
						for _, ref := range hookResult.Media {
							part := bus.MediaPart{Ref: ref}
							if mediaStore != nil {
								if _, meta, err := mediaStore.ResolveWithMeta(ref); err == nil {
									part.Filename = meta.Filename
									part.ContentType = meta.ContentType
									part.Type = inferMediaType(meta.Filename, meta.ContentType)
								}
							}
							parts = append(parts, part)
						}
						outboundMedia := bus.OutboundMediaMessage{
							Channel: ts.channel,
							ChatID:  ts.chatID,
							Context: outboundContextFromInbound(
								ts.opts.Dispatch.InboundContext,
								ts.channel,
								ts.chatID,
								ts.opts.Dispatch.ReplyToMessageID(),
							),
							AgentID:    ts.agent.ID,
							SessionKey: ts.sessionKey,
							Scope:      outboundScopeFromSessionScope(ts.opts.Dispatch.SessionScope),
							Parts:      parts,
						}
						if channelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
							if err := channelManager.SendMedia(ctx, outboundMedia); err != nil {
								logger.WarnCF("agent", "Failed to deliver hook media",
									map[string]any{
										"agent_id": ts.agent.ID,
										"tool":     toolName,
										"channel":  ts.channel,
										"chat_id":  ts.chatID,
										"error":    err.Error(),
									})
								hookResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
							} else {
								handledAttachments = append(
									handledAttachments,
									buildProviderAttachments(mediaStore, hookResult.Media)...,
								)
							}
						} else if al.bus != nil {
							hookResult.ResponseHandled = false
							if err := al.bus.PublishOutboundMedia(ctx, outboundMedia); err != nil {
								hookResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
							}
						}
					}

					if !hookResult.ResponseHandled {
						exec.allResponsesHandled = false
					}

					contentForLLM := hookResult.ContentForLLM()
					if cfg.Tools.IsFilterSensitiveDataEnabled() {
						contentForLLM = cfg.FilterSensitiveData(contentForLLM)
					}

					var toolResultMedia []string
					if len(hookResult.Media) > 0 && !hookResult.ResponseHandled {
						hookResult.ArtifactTags = buildArtifactTags(mediaStore, hookResult.Media)
						contentForLLM = hookResult.ContentForLLM()
						if cfg.Tools.IsFilterSensitiveDataEnabled() {
							contentForLLM = cfg.FilterSensitiveData(contentForLLM)
						}
						toolResultMedia = append(toolResultMedia, hookResult.Media...)
					}
					toolResultMsg := toolResultPromptMessage(contentForLLM, tc.ID, toolResultMedia)

					al.emitEvent(
						runtimeevents.KindAgentToolExecEnd,
						ts.eventMeta("runTurn", "turn.tool.end"),
						ToolExecEndPayload{
							CallID:     tc.ID,
							Tool:       toolName,
							Duration:   toolDuration,
							ForLLMLen:  len(contentForLLM),
							ForUserLen: len(hookResult.ForUser),
							IsError:    hookResult.IsError,
							Async:      hookResult.Async,
							Error:      toolErrorSummary(cfg, hookResult),
						},
					)
					ts.recordToolExecution(
						toolName,
						!hookResult.IsError,
						toolErrorSummary(cfg, hookResult),
						inferSkillNamesFromToolCall(ts, toolName, toolArgs),
					)
					ts.finishToolItemFromResult(al, toolBatchID, i, toolPolicy, hookResult, false)

					messages = append(messages, toolResultMsg)
					if !ts.opts.NoHistory {
						if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, toolResultMsg); err != nil {
							exec.executionErr = fmt.Errorf("write tool result: %w", err)
							return ToolControlBreak
						}
						ts.recordPersistedMessage(toolResultMsg)
						if err := ts.ingestMessage(turnCtx, al, toolResultMsg); err != nil {
							exec.executionErr = err
							return ToolControlBreak
						}
						if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
							exec.executionErr = fmt.Errorf("save tool result: %w", err)
							return ToolControlBreak
						}
					}

					if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
						exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
					}

					skipReason := ""
					skipMessage := ""
					if len(exec.pendingMessages) > 0 {
						skipReason = "queued user steering message"
						skipMessage = "Skipped due to queued user message."
					} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
						skipReason = "graceful interrupt requested"
						skipMessage = "Skipped due to graceful interrupt."
					}

					if skipReason != "" && preExecutedResults == nil {
						remaining := len(normalizedToolCalls) - i - 1
						if remaining > 0 {
							logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools after hook respond",
								map[string]any{
									"agent_id":  ts.agent.ID,
									"completed": i + 1,
									"skipped":   remaining,
									"reason":    skipReason,
								})
							for j := i + 1; j < len(normalizedToolCalls); j++ {
								skippedTC := normalizedToolCalls[j]
								ts.markToolItemTerminal(toolBatchID, j, toolItemStatusSkipped, toolItemSideEffectNone, skipMessage)
								al.emitEvent(
									runtimeevents.KindAgentToolExecSkipped,
									ts.eventMeta("runTurn", "turn.tool.skipped"),
									ToolExecSkippedPayload{
										CallID: skippedTC.ID,
										Tool:   skippedTC.Name,
										Reason: skipReason,
									},
								)
								skippedMsg := providers.Message{
									Role:       "tool",
									Content:    skipMessage,
									ToolCallID: skippedTC.ID,
								}
								messages = append(messages, skippedMsg)
								if !ts.opts.NoHistory {
									if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, skippedMsg); err != nil {
										exec.executionErr = fmt.Errorf("write skipped tool result: %w", err)
										return ToolControlBreak
									}
									ts.recordPersistedMessage(skippedMsg)
								}
							}
						}
						break toolLoop
					}

					continue
				}
				logger.WarnCF("agent", "Hook returned respond action but no HookResult provided",
					map[string]any{
						"agent_id": ts.agent.ID,
						"tool":     toolName,
						"action":   "respond",
					})
			case HookActionDenyTool:
				exec.allResponsesHandled = false
				denyContent := hookDeniedToolContent("Tool execution denied by hook", decision.Reason)
				ts.markToolItemTerminal(toolBatchID, i, toolItemStatusDenied, toolItemSideEffectNone, denyContent)
				al.emitEvent(
					runtimeevents.KindAgentToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						CallID: tc.ID,
						Tool:   toolName,
						Reason: denyContent,
					},
				)
				deniedMsg := providers.Message{
					Role:       "tool",
					Content:    denyContent,
					ToolCallID: tc.ID,
				}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, deniedMsg); err != nil {
						exec.executionErr = fmt.Errorf("write denied tool result: %w", err)
						return ToolControlBreak
					}
					ts.recordPersistedMessage(deniedMsg)
				}
				continue
			case HookActionAbortTurn:
				ts.markToolItemTerminal(toolBatchID, i, toolItemStatusCancelled, toolItemSideEffectNone, "Tool cancelled by hook.")
				exec.abortedByHook = true
				return ToolControlBreak
			case HookActionHardAbort:
				ts.markToolItemTerminal(toolBatchID, i, toolItemStatusCancelled, toolItemSideEffectNone, "Tool cancelled by hook.")
				_ = ts.requestHardAbort()
				exec.abortedByHardAbort = true
				return ToolControlBreak
			}
		}

		humanApprovalRequired, _, humanApprovalReason := tools.StaticApprovalPolicy(
			cfg.Tools.Approval, al.publicExposure, toolName, toolArgs,
		)
		if al.hooks != nil {
			approval := al.hooks.ApproveTool(turnCtx, &ToolApprovalRequest{
				Meta:       ts.eventMeta("runTurn", "turn.tool.approve"),
				Context:    cloneTurnContext(ts.turnCtx),
				Tool:       toolName,
				ToolCallID: tc.ID,
				Arguments:  toolArgs,
			})
			if approval.RequiresHuman {
				humanApprovalRequired = true
				humanApprovalReason = approval.Reason
			} else if !approval.Approved {
				exec.allResponsesHandled = false
				denyContent := hookDeniedToolContent("Tool execution denied by approval hook", approval.Reason)
				ts.markToolItemTerminal(toolBatchID, i, toolItemStatusDenied, toolItemSideEffectNone, denyContent)
				al.emitEvent(
					runtimeevents.KindAgentToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						CallID: tc.ID,
						Tool:   toolName,
						Reason: denyContent,
					},
				)
				deniedMsg := providers.Message{
					Role:       "tool",
					Content:    denyContent,
					ToolCallID: tc.ID,
				}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, deniedMsg); err != nil {
						exec.executionErr = fmt.Errorf("write denied tool result: %w", err)
						return ToolControlBreak
					}
					ts.recordPersistedMessage(deniedMsg)
				}
				continue
			}
		}
		// Applied after hook deny handling so explicit denials still win.
		if cfg.Tools.Approval.SkipsAllApproval() {
			humanApprovalRequired = false
		}
		if humanApprovalRequired {
			ts.markToolItemAwaitingApproval(toolBatchID, i, toolName, toolArgs)
			if !ts.opts.NoHistory {
				if err := ts.saveTurnCheckpoint("awaiting_approval", toolName); err != nil {
					exec.executionErr = fmt.Errorf("save approval checkpoint: %w", err)
					return ToolControlBreak
				}
			}
			decision, err := al.requestHumanApproval(turnCtx, ts, toolName, tc.ID, toolArgs, humanApprovalReason)
			if err != nil {
				exec.executionErr = fmt.Errorf("wait for human approval: %w", err)
				return ToolControlBreak
			}
			if !decision.Approved {
				exec.allResponsesHandled = false
				denyContent := hookDeniedToolContent("Tool execution denied by human approval", decision.Reason)
				ts.markToolItemTerminal(toolBatchID, i, toolItemStatusDenied, toolItemSideEffectNone, denyContent)
				al.emitEvent(
					runtimeevents.KindAgentToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{CallID: tc.ID, Tool: toolName, Reason: denyContent},
				)
				deniedMsg := providers.Message{Role: "tool", Content: denyContent, ToolCallID: tc.ID}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, deniedMsg); err != nil {
						exec.executionErr = fmt.Errorf("write denied tool result: %w", err)
						return ToolControlBreak
					}
					ts.recordPersistedMessage(deniedMsg)
				}
				continue
			}
		}

		if denyByTurnProfile() {
			if exec.executionErr != nil {
				return ToolControlBreak
			}
			continue
		}

		argsJSON, _ := json.Marshal(toolArgs)
		argsPreview := utils.Truncate(cfg.RedactDiagnostic(string(argsJSON)), 200)
		executedToolName := toolName
		toolPolicy := ts.toolPolicy(executedToolName)
		ts.markToolItemRunning(toolBatchID, i, toolName, toolArgs)
		readOnlyKey := toolCallFingerprint(executedToolName, toolArgs)
		duplicateReadOnly := false
		duplicateRepeat := 0
		if preExecutedResults == nil && toolPolicy.ReadOnly && toolPolicy.Idempotent && ts.readOnlyCallSeen(readOnlyKey) {
			duplicateReadOnly = true
			duplicateRepeat = ts.recordReadOnlyRepeat(readOnlyKey)
		}
		logger.InfoCF("agent", "Tool call",
			map[string]any{
				"turn_id":   ts.turnID,
				"agent_id":  ts.agent.ID,
				"tool":      toolName,
				"iteration": iteration,
			})
		if preExecutedResults == nil {
			al.emitEvent(
				runtimeevents.KindAgentToolExecStart,
				ts.eventMeta("runTurn", "turn.tool.start"),
				ToolExecStartPayload{
					CallID:    tc.ID,
					Tool:      toolName,
					Arguments: cloneEventArguments(toolArgs),
				},
			)
		}
		activityID := ""
		if preExecutedResults != nil {
			activityID = parallelActivities[i]
		} else {
			activityID = ts.startTurnActivity("tool", toolName, toolCallIDFromCall(tc), toolName, argsPreview)
		}

		if !duplicateReadOnly && shouldPublishToolFeedback(cfg, ts) {
			toolFeedbackMaxLen := cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength()
			toolFeedbackExplanation := exec.toolFeedbackExplanation(tc, messages)
			feedbackMsg := utils.FormatToolFeedbackMessage(
				toolName,
				toolFeedbackExplanation,
				toolFeedbackArgsPreview(toolArgs, toolFeedbackMaxLen),
			)
			fbCtx, fbCancel := context.WithTimeout(turnCtx, 3*time.Second)
			_ = al.bus.PublishOutbound(fbCtx, outboundMessageForTurnWithOptions(
				ts,
				cfg.RedactDiagnostic(feedbackMsg),
				outboundTurnMessageOptions{kind: messageKindToolFeedback},
			))
			fbCancel()
		}

		toolCallID := toolCallIDFromCall(tc)
		asyncToolName := toolName
		asyncCallback := func(_ context.Context, result *tools.ToolResult) {
			if result == nil {
				return
			}
			al.deliverAsyncResult(ts, asyncDelivery{
				Origin:         asyncOriginTool,
				Source:         asyncToolName,
				ToolCallID:     toolCallID,
				Result:         result,
				PublishForUser: true,
			})
		}

		toolStart := time.Now()
		if !ts.opts.NoHistory {
			if err := ts.saveTurnCheckpoint("running", toolName); err != nil {
				exec.executionErr = fmt.Errorf("save tool checkpoint: %w", err)
				return ToolControlBreak
			}
		}
		execCtx := tools.WithToolInboundContext(
			turnCtx,
			ts.channel,
			ts.chatID,
			ts.opts.Dispatch.MessageID(),
			ts.opts.Dispatch.ReplyToMessageID(),
		)
		execCtx = tools.WithToolSessionContext(
			execCtx,
			ts.agent.ID,
			ts.sessionKey,
			ts.opts.Dispatch.SessionScope,
		)
		execCtx = tools.WithToolCallID(execCtx, toolCallID)
		var toolResult *tools.ToolResult
		if duplicateReadOnly {
			content := "This exact read-only call already ran successfully earlier in this turn. It was not executed again. Use the earlier result instead of repeating the call."
			if duplicateRepeat > 1 {
				content = "This exact read-only call was already skipped once. Do not repeat it again. Use the earlier results or explain what information is still missing."
			}
			toolResult = tools.SilentResult(content)
		} else if preExecutedResults != nil && preExecutedResults[i] != nil {
			toolResult = preExecutedResults[i]
		} else {
			toolResult = ts.toolRegistry().ExecuteWithContext(
				execCtx,
				toolName,
				toolArgs,
				ts.channel,
				ts.chatID,
				asyncCallback,
			)
		}
		toolDuration := time.Since(toolStart)
		if preExecutedResults != nil {
			toolDuration = parallelDurations[i]
		}

		if ts.hardAbortRequested() {
			ts.markToolItemTerminal(toolBatchID, i, toolItemStatusEffectsUnknown, toolItemSideEffectUnknown, "The turn was stopped during tool execution; external effects could not be verified.")
			ts.finishTurnActivity(activityID, func(activity *session.TurnActivity) {
				activity.Status = "effects_unknown"
				if toolResult != nil {
					activity.Output = boundedActivityOutput(
						cfg.RedactDiagnostic(toolResult.ContentForLLM()),
						cfg.RedactDiagnostic(toolResult.ForUser),
					)
				}
				activity.Detail = strings.TrimSpace(activity.Detail +
					"\nThe turn was stopped during tool execution; external effects could not be verified.")
			})
			exec.abortedByHardAbort = true
			return ToolControlBreak
		}

		if !duplicateReadOnly && al.hooks != nil {
			toolResp, decision := al.hooks.AfterTool(turnCtx, &ToolResultHookResponse{
				Meta:      ts.eventMeta("runTurn", "turn.tool.after"),
				Context:   cloneTurnContext(ts.turnCtx),
				Tool:      toolName,
				Arguments: toolArgs,
				Result:    toolResult,
				Duration:  toolDuration,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if toolResp != nil {
					if toolResp.Tool != "" {
						toolName = toolResp.Tool
					}
					if toolResp.Result != nil {
						toolResult = toolResp.Result
					}
				}
			case HookActionAbortTurn:
				ts.finishToolItemFromResult(al, toolBatchID, i, ts.toolPolicy(toolName), toolResult, duplicateReadOnly)
				exec.abortedByHook = true
				return ToolControlBreak
			case HookActionHardAbort:
				ts.finishToolItemFromResult(al, toolBatchID, i, ts.toolPolicy(toolName), toolResult, duplicateReadOnly)
				_ = ts.requestHardAbort()
				exec.abortedByHardAbort = true
				return ToolControlBreak
			}
		}

		if toolResult == nil {
			toolResult = tools.ErrorResult("hook returned nil tool result")
		}
		al.pinTurnMedia(ts, toolResult.Media)
		if len(toolResult.Media) > 0 && toolResult.ResponseHandled {
			parts := make([]bus.MediaPart, 0, len(toolResult.Media))
			for _, ref := range toolResult.Media {
				part := bus.MediaPart{Ref: ref}
				if mediaStore != nil {
					if _, meta, err := mediaStore.ResolveWithMeta(ref); err == nil {
						part.Filename = meta.Filename
						part.ContentType = meta.ContentType
						part.Type = inferMediaType(meta.Filename, meta.ContentType)
					}
				}
				parts = append(parts, part)
			}
			outboundMedia := bus.OutboundMediaMessage{
				Channel: ts.channel,
				ChatID:  ts.chatID,
				Context: outboundContextFromInbound(
					ts.opts.Dispatch.InboundContext,
					ts.channel,
					ts.chatID,
					ts.opts.Dispatch.ReplyToMessageID(),
				),
				AgentID:    ts.agent.ID,
				SessionKey: ts.sessionKey,
				Scope:      outboundScopeFromSessionScope(ts.opts.Dispatch.SessionScope),
				Parts:      parts,
			}
			if channelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
				if err := channelManager.SendMedia(ctx, outboundMedia); err != nil {
					logger.WarnCF("agent", "Failed to deliver handled tool media",
						map[string]any{
							"agent_id": ts.agent.ID,
							"tool":     toolName,
							"channel":  ts.channel,
							"chat_id":  ts.chatID,
							"error":    err.Error(),
						})
					toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
				} else {
					handledAttachments = append(
						handledAttachments,
						buildProviderAttachments(mediaStore, toolResult.Media)...,
					)
				}
			} else if al.bus != nil {
				toolResult.ResponseHandled = false
				if err := al.bus.PublishOutboundMedia(ctx, outboundMedia); err != nil {
					toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
				}
			}
		}

		ts.finishToolItemFromResult(al, toolBatchID, i, ts.toolPolicy(toolName), toolResult, duplicateReadOnly)
		if !duplicateReadOnly && !toolResult.IsError && toolPolicy.ReadOnly && toolPolicy.Idempotent {
			ts.recordReadOnlyCall(readOnlyKey)
		}
		if !toolPolicy.ReadOnly {
			ts.invalidateReadOnlyCalls()
		}
		if duplicateReadOnly && duplicateRepeat >= ts.budget.Progress.ReadOnlyReplayStopAfter {
			ts.setEndReason(TurnEndReasonReadOnlyReplay)
			ts.requestGracefulInterrupt("The model repeated the same read-only tool call. Use the evidence already collected or explain the blocker instead of retrying it.")
		}
		progress := ts.recordToolProgress(readOnlyKey, toolPolicy, toolResult, duplicateReadOnly)
		progressDecision := ts.evaluateToolProgress(progress, !duplicateReadOnly && toolPolicy.Retrieval)
		if progressDecision.Hint != "" {
			toolResult.ForLLM = toolResult.ContentForLLM() + progressDecision.Hint
		}
		if progressDecision.EndReason != "" {
			ts.setEndReason(progressDecision.EndReason)
			_ = ts.requestGracefulInterrupt(progressDecision.InterruptHint)
		}
		failureKey := toolName + "\x00" + string(argsJSON)
		failures := ts.recordRepeatedFailure(failureKey, toolResult.IsError)
		failureDecision := ts.evaluateRepeatedFailure(failures)
		if failureDecision.Hint != "" {
			toolResult.ForLLM = toolResult.ContentForLLM() + failureDecision.Hint
		}
		if failureDecision.EndReason != "" {
			ts.setEndReason(failureDecision.EndReason)
			_ = ts.requestGracefulInterrupt(failureDecision.InterruptHint)
		}

		if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
			toolResult.ArtifactTags = buildArtifactTags(mediaStore, toolResult.Media)
		}

		if !toolResult.ResponseHandled {
			exec.allResponsesHandled = false
		}

		shouldSendForUser := !toolResult.Silent &&
			toolResult.ForUser != "" &&
			(ts.opts.SendResponse || toolResult.ResponseHandled)
		if shouldSendForUser {
			al.bus.PublishOutbound(ctx, outboundMessageForTurn(ts, cfg.RedactDiagnostic(toolResult.ForUser)))
			logger.DebugCF("agent", "Sent tool result to user",
				map[string]any{
					"tool":        toolName,
					"content_len": len(toolResult.ForUser),
				})
		}
		contentForLLM := toolResult.ContentForLLM()

		if cfg.Tools.IsFilterSensitiveDataEnabled() {
			contentForLLM = cfg.FilterSensitiveData(contentForLLM)
		}
		compacted := al.compactToolResultForPrompt(ts, toolName, toolCallID, contentForLLM)
		contentForLLM = compacted.Content
		if compacted.Artifact != "" {
			toolResult.ArtifactTags = append(
				toolResult.ArtifactTags,
				"[file:"+compacted.Artifact+"]",
			)
		}

		var toolResultMedia []string
		if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
			toolResultMedia = append(toolResultMedia, toolResult.Media...)
		}
		toolResultMsg := toolResultPromptMessage(contentForLLM, toolCallID, toolResultMedia)
		if preExecutedResults == nil {
			al.emitEvent(
				runtimeevents.KindAgentToolExecEnd,
				ts.eventMeta("runTurn", "turn.tool.end"),
				ToolExecEndPayload{
					CallID:     toolCallID,
					Tool:       toolName,
					Duration:   toolDuration,
					ForLLMLen:  len(contentForLLM),
					ForUserLen: len(toolResult.ForUser),
					IsError:    toolResult.IsError,
					Async:      toolResult.Async,
					Error:      toolErrorSummary(cfg, toolResult),
				},
			)
		}
		if duplicateReadOnly {
			ts.recordToolKind(toolName)
		} else {
			ts.recordToolExecution(
				toolName,
				!toolResult.IsError,
				toolErrorSummary(cfg, toolResult),
				inferSkillNamesFromToolCall(ts, toolName, toolArgs),
			)
		}
		ts.finishTurnActivity(activityID, func(activity *session.TurnActivity) {
			activity.Status = "completed"
			if duplicateReadOnly {
				activity.Status = "skipped"
			} else if toolResult.IsError {
				activity.Status = "failed"
			}
			filteredForLLM := cfg.RedactDiagnostic(toolResult.ForLLM)
			filteredForUser := cfg.RedactDiagnostic(toolResult.ForUser)
			activity.Output = boundedActivityOutput(filteredForLLM, filteredForUser)
			activity.Diff = activityDiffPreview(filteredForUser)
			activity.ExitCode = activityExitCode(toolResult.ForLLM)
			activity.Artifacts = append([]string(nil), toolResult.ArtifactTags...)
		})
		messages = append(messages, toolResultMsg)
		if !ts.opts.NoHistory {
			if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, toolResultMsg); err != nil {
				exec.executionErr = fmt.Errorf("write tool result: %w", err)
				return ToolControlBreak
			}
			ts.recordPersistedMessage(toolResultMsg)
			if err := ts.ingestMessage(turnCtx, al, toolResultMsg); err != nil {
				exec.executionErr = err
				return ToolControlBreak
			}
			if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
				exec.executionErr = fmt.Errorf("save tool result: %w", err)
				return ToolControlBreak
			}
			if err := ts.saveTurnCheckpoint("running", ""); err != nil {
				exec.executionErr = fmt.Errorf("save tool checkpoint: %w", err)
				return ToolControlBreak
			}
		}

		if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
			exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		}

		skipReason := ""
		skipMessage := ""
		if len(exec.pendingMessages) > 0 {
			skipReason = "queued user steering message"
			skipMessage = "Skipped due to queued user message."
		} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
			skipReason = "graceful interrupt requested"
			skipMessage = "Skipped due to graceful interrupt."
		}

		if skipReason != "" && preExecutedResults == nil {
			remaining := len(normalizedToolCalls) - i - 1
			if remaining > 0 {
				logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"completed": i + 1,
						"skipped":   remaining,
						"reason":    skipReason,
					})
				for j := i + 1; j < len(normalizedToolCalls); j++ {
					skippedTC := normalizedToolCalls[j]
					ts.markToolItemTerminal(toolBatchID, j, toolItemStatusSkipped, toolItemSideEffectNone, skipMessage)
					al.emitEvent(
						runtimeevents.KindAgentToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							CallID: skippedTC.ID,
							Tool:   skippedTC.Name,
							Reason: skipReason,
						},
					)
					skippedMsg := providers.Message{
						Role:       "tool",
						Content:    skipMessage,
						ToolCallID: skippedTC.ID,
					}
					messages = append(messages, skippedMsg)
					if !ts.opts.NoHistory {
						if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, skippedMsg); err != nil {
							exec.executionErr = fmt.Errorf("write skipped tool result: %w", err)
							return ToolControlBreak
						}
						ts.recordPersistedMessage(skippedMsg)
					}
				}
			}
			break toolLoop
		}

	}

	exec.messages = messages
	if ts.hasPendingAsyncDeliveries() {
		// A background task completed while tools were running. Return to the
		// model so the single delivery consumer injects it before finalization.
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// Continue if pending steering exists (regardless of allResponsesHandled).
	// This covers the case where tools were partially executed and skipped due to steering,
	// but one tool had ResponseHandled=false (so allResponsesHandled=false).
	if len(exec.pendingMessages) > 0 {
		logger.InfoCF("agent", "Pending steering after partial tool execution; continuing turn",
			map[string]any{
				"agent_id":            ts.agent.ID,
				"pending_count":       len(exec.pendingMessages),
				"allResponsesHandled": exec.allResponsesHandled,
			})
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// Poll for newly arrived steering
	if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after tool delivery; continuing turn",
			map[string]any{
				"agent_id":       ts.agent.ID,
				"steering_count": len(steerMsgs),
			})
		exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// No pending steering: finalize or break depending on allResponsesHandled
	if exec.allResponsesHandled {
		summaryMsg := providers.Message{
			Role:        "assistant",
			Content:     handledToolResponseSummary,
			Attachments: append([]providers.Attachment(nil), handledAttachments...),
		}
		if !ts.opts.NoHistory {
			if err := session.AppendChecked(ts.agent.Sessions, ts.sessionKey, summaryMsg); err != nil {
				exec.executionErr = fmt.Errorf("write delivered result: %w", err)
				return ToolControlBreak
			}
			ts.recordPersistedMessage(summaryMsg)
			if err := ts.ingestMessage(turnCtx, al, summaryMsg); err != nil {
				exec.executionErr = err
				return ToolControlBreak
			}
			if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
				exec.executionErr = fmt.Errorf("save delivered result: %w", err)
				return ToolControlBreak
			}
		}
		if !ts.opts.NoHistory && ts.opts.EnableSummary {
			if err := p.ContextManager.Compact(turnCtx, &CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			}); err != nil {
				logger.WarnCF("agent", "Post-tool compaction failed", map[string]any{
					"session_key": ts.sessionKey,
					"turn_id":     ts.turnID,
					"error":       err.Error(),
				})
			}
		}
		ts.setFinalContent("")
		if channelManager != nil && ts.channel != "" {
			channelManager.DismissToolFeedback(ctx, ts.channel, ts.chatID, ts.opts.InboundContext)
		}
		logger.InfoCF("agent", "Tool output satisfied delivery; ending turn without follow-up LLM",
			map[string]any{
				"agent_id":   ts.agent.ID,
				"iteration":  iteration,
				"tool_count": len(normalizedToolCalls),
			})
		return ToolControlBreak
	}

	// allResponsesHandled=false and no pending steering: continue so coordinator
	// makes another LLM call. The tool result is in messages and the LLM will
	// return it as finalContent in the next iteration.
	// A discovery result must remain available for the next model call even
	// when its configured lifetime is one iteration.
	discovered := false
	for _, tc := range normalizedToolCalls {
		if tc.Name == tools.BM25SearchToolName {
			discovered = true
			break
		}
	}
	if !discovered {
		ts.toolRegistry().TickTTL()
	}
	logger.DebugCF("agent", "TTL tick after tool execution", map[string]any{
		"agent_id": ts.agent.ID, "iteration": iteration,
	})
	return ToolControlContinue
}
