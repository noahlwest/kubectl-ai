// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/kubectl-ai/gollm"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/api"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/journal"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/mcp"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/sessions"
	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/tools"
	"github.com/google/uuid"
	"k8s.io/klog/v2"
)

//go:embed systemprompt_template_default.txt
var defaultSystemPromptTemplate string

type Agent struct {
	// Input is the channel to receive user input.
	Input chan any

	// Output is the channel to send messages to the UI.
	Output chan any

	// RunOnce indicates if the agent should run only once.
	// If true, the agent will run only once and then exit.
	// If false, the agent will run in a loop until the context is done.
	RunOnce bool

	// InitialQuery is the initial query to the agent.
	// If provided, the agent will run only once and then exit.
	InitialQuery string

	// tool calls that are pending execution
	// These will typically be all the tool calls suggested by the LLM in the
	// previous iteration of the agentic loop.
	pendingFunctionCalls []ToolCallAnalysis

	// currChatContent tracks chat content that needs to be sent
	// to the LLM in the current iteration of the agentic loop.
	currChatContent []any

	// currIteration tracks the current iteration of the agentic loop.
	currIteration int

	LLM gollm.Client

	// PromptTemplateFile allows specifying a custom template file
	PromptTemplateFile string
	// ExtraPromptPaths allows specifying additional prompt templates
	// to be combined with PromptTemplateFile
	ExtraPromptPaths []string
	Model            string

	RemoveWorkDir bool

	MaxIterations int

	// Kubeconfig is the path to the kubeconfig file.
	Kubeconfig string

	SkipPermissions bool

	Tools tools.Tools

	EnableToolUseShim bool

	// MCPClientEnabled indicates whether MCP client mode is enabled
	MCPClientEnabled bool

	// Recorder captures events for diagnostics
	Recorder journal.Recorder

	// ChatMessageStore is the underlying session persistence layer.
	ChatMessageStore sessions.ChatMessageStore

	llmChat gollm.Chat

	workDir string

	// session tracks the current session of the agent
	// this is used by the UI to track the state of the agent and the conversation
	session *api.Session

	// protects session from concurrent access
	sessionMu sync.Mutex

	// cached list of available models
	availableModels []string

	// mcpManager manages MCP client connections
	mcpManager *mcp.Manager
}

func (s *Agent) Session() *api.Session {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	// Create a shallow copy of the session struct. The Messages slice header
	// is also copied, providing the caller with a snapshot of the messages
	// at this point in time. The UI should treat the messages as read-only
	// to avoid race conditions.
	sessionCopy := *s.session
	return &sessionCopy
}

// addMessage creates a new message, adds it to the session, and sends it to the output channel
func (c *Agent) addMessage(source api.MessageSource, messageType api.MessageType, payload any) *api.Message {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	message := &api.Message{
		ID:        uuid.New().String(),
		Source:    source,
		Type:      messageType,
		Payload:   payload,
		Timestamp: time.Now(),
	}
	c.session.Messages = append(c.session.Messages, message)
	c.session.LastModified = time.Now()
	c.Output <- message
	return message
}

// setAgentState updates the agent state and ensures LastModified is updated
func (c *Agent) setAgentState(newState api.AgentState) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	currentState := c.agentState()
	if currentState != newState {
		klog.Infof("Agent state changing from %s to %s", currentState, newState)
		c.session.AgentState = newState
		c.session.LastModified = time.Now()
	}
}

func (c *Agent) AgentState() api.AgentState {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	return c.agentState()
}

// agentState returns the agent state without locking.
// The caller is responsible for locking.
func (c *Agent) agentState() api.AgentState {
	return c.session.AgentState
}

func (s *Agent) Init(ctx context.Context) error {
	log := klog.FromContext(ctx)

	s.Input = make(chan any, 10)
	s.Output = make(chan any, 10)
	s.currIteration = 0
	// when we support session, we will need to initialize this with the
	// current history of the conversation.
	s.currChatContent = []any{}

	if s.InitialQuery == "" && s.RunOnce {
		return fmt.Errorf("RunOnce mode requires an initial query to be provided")
	}

	// TODO: this is ephemeral for now, but in the future, we will support
	// session persistence.
	sessionFromStore, ok := s.ChatMessageStore.(*sessions.Session)
	if ok {
		sessionMetadata, err := sessionFromStore.LoadMetadata()
		if err != nil {
			return fmt.Errorf("Failed to load metadata for ChatMessageStore")
		}
		s.session = &api.Session{
			ID: sessionFromStore.ID,
			Messages: []*api.Message{},
			AgentState: api.AgentStateIdle,
			CreatedAt: sessionMetadata.CreatedAt,
			LastModified: sessionMetadata.LastAccessed,
			SessionDB: s.ChatMessageStore.(*sessions.Session),
		}
	} else {
		return fmt.Errorf("Failed to load session while Initializing")
	}

	// Create a temporary working directory
	workDir, err := os.MkdirTemp("", "agent-workdir-*")
	if err != nil {
		log.Error(err, "Failed to create temporary working directory")
		return err
	}

	log.Info("Created temporary working directory", "workDir", workDir)

	systemPrompt, err := s.generatePrompt(ctx, defaultSystemPromptTemplate, PromptData{
		Tools:             s.Tools,
		EnableToolUseShim: s.EnableToolUseShim,
	})
	if err != nil {
		return fmt.Errorf("generating system prompt: %w", err)
	}

	// Start a new chat session
	chat := s.LLM.StartChat(systemPrompt, s.Model)

	if s.ChatMessageStore != nil {
		messages := s.ChatMessageStore.ChatMessages()
		if len(messages) > 0 {
			if err := chat.Initialize(messages); err != nil {
				return fmt.Errorf("loading history into chat: %w", err)
			}
		}
		chat = NewChatLogger(chat, s.ChatMessageStore)
	}

	s.llmChat = gollm.NewRetryChat(
		chat,
		gollm.RetryConfig{
			MaxAttempts:    3,
			InitialBackoff: 10 * time.Second,
			MaxBackoff:     60 * time.Second,
			BackoffFactor:  2,
			Jitter:         true,
		},
	)

	if !s.EnableToolUseShim {
		var functionDefinitions []*gollm.FunctionDefinition
		for _, tool := range s.Tools.AllTools() {
			functionDefinitions = append(functionDefinitions, tool.FunctionDefinition())
		}
		// Sort function definitions to help KV cache reuse
		sort.Slice(functionDefinitions, func(i, j int) bool {
			return functionDefinitions[i].Name < functionDefinitions[j].Name
		})
		if err := s.llmChat.SetFunctionDefinitions(functionDefinitions); err != nil {
			return fmt.Errorf("setting function definitions: %w", err)
		}
	}
	s.workDir = workDir

	// Initialize MCP client if enabled
	if s.MCPClientEnabled {
		if err := s.InitializeMCPClient(ctx); err != nil {
			klog.Errorf("Failed to initialize MCP client: %v", err)
			return fmt.Errorf("failed to initialize MCP client: %w", err)
		}

		// Update MCP status in session
		if err := s.UpdateMCPStatus(ctx, s.MCPClientEnabled); err != nil {
			klog.Warningf("Failed to update MCP status: %v", err)
		}
	}

	return nil
}

func (c *Agent) Close() error {
	if c.workDir != "" {
		if c.RemoveWorkDir {
			if err := os.RemoveAll(c.workDir); err != nil {
				klog.Warningf("error cleaning up directory %q: %v", c.workDir, err)
			}
		}
	}
	// Close MCP client connections
	if err := c.CloseMCPClient(); err != nil {
		klog.Warningf("error closing MCP client: %v", err)
	}
	return nil
}

func (c *Agent) Run(ctx context.Context, initialQuery string) error {
	log := klog.FromContext(ctx)

	log.Info("Starting agent loop", "initialQuery", initialQuery, "runOnce", c.RunOnce)
	go func() {
		if initialQuery != "" {
			c.addMessage(api.MessageSourceUser, api.MessageTypeText, initialQuery)
			answer, handled, err := c.handleMetaQuery(ctx, initialQuery)
			if err != nil {
				log.Error(err, "error handling meta query")
				c.setAgentState(api.AgentStateDone)
				c.pendingFunctionCalls = []ToolCallAnalysis{}
				c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: "+err.Error())
			} else if handled {
				// we handled the meta query, so we don't need to run the agentic loop
				c.setAgentState(api.AgentStateDone)
				c.pendingFunctionCalls = []ToolCallAnalysis{}
				c.addMessage(api.MessageSourceAgent, api.MessageTypeText, answer)
			} else {
				// Start the agentic loop with the initial query
				c.setAgentState(api.AgentStateRunning)
				c.currIteration = 0
				c.currChatContent = []any{initialQuery}
				c.pendingFunctionCalls = []ToolCallAnalysis{}
			}
		} else {
			greetingMessage := "Hey there, what can I help you with today ?"
			c.addMessage(api.MessageSourceAgent, api.MessageTypeUserInputRequest, greetingMessage)
		}
		for {
			var userInput any
			log.Info("Agent loop iteration", "state", c.AgentState())
			switch c.AgentState() {
			case api.AgentStateIdle, api.AgentStateDone:
				// In RunOnce mode, we are done, so exit
				if c.RunOnce {
					log.Info("RunOnce mode, exiting agent loop")
					c.setAgentState(api.AgentStateExited)
					return
				}
				log.Info("initiating user input")
				c.addMessage(api.MessageSourceAgent, api.MessageTypeUserInputRequest, ">>>")
				select {
				case <-ctx.Done():
					log.Info("Agent loop done")
					return
				case userInput = <-c.Input:
					log.Info("Received input from channel", "userInput", userInput)
					if userInput == io.EOF {
						log.Info("Agent loop done, EOF received")
						c.setAgentState(api.AgentStateExited)
						return
					}
					query, ok := userInput.(*api.UserInputResponse)
					if !ok {
						log.Error(nil, "Received unexpected input from channel", "userInput", userInput)
						return
					}
					c.addMessage(api.MessageSourceUser, api.MessageTypeText, query.Query)
					// we don't need the agentic loop for meta queries
					// for ex. model, tools, etc.
					answer, handled, err := c.handleMetaQuery(ctx, query.Query)
					if err != nil {
						log.Error(err, "error handling meta query")
						c.setAgentState(api.AgentStateDone)
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: "+err.Error())
						continue
					}
					if handled {
						// we handled the meta query, so we don't need to run the agentic loop
						c.setAgentState(api.AgentStateDone)
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						c.addMessage(api.MessageSourceAgent, api.MessageTypeText, answer)
						continue
					}

					c.setAgentState(api.AgentStateRunning)
					c.currIteration = 0
					c.currChatContent = []any{query.Query}
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					log.Info("Set agent state to running, will process agentic loop", "currIteration", c.currIteration, "currChatContent", len(c.currChatContent))
				}
			case api.AgentStateWaitingForInput:
				// In RunOnce mode, if we need user choice, exit with error
				if c.RunOnce {
					log.Error(nil, "RunOnce mode cannot handle user choice requests")
					c.setAgentState(api.AgentStateExited)
					c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: RunOnce mode cannot handle user choice requests")
					return
				}
				select {
				case <-ctx.Done():
					log.Info("Agent loop done")
					return
				case userInput = <-c.Input:
					if userInput == io.EOF {
						log.Info("Agent loop done, EOF received")
						c.setAgentState(api.AgentStateExited)
						return
					}
					choiceResponse, ok := userInput.(*api.UserChoiceResponse)
					if !ok {
						log.Error(nil, "Received unexpected input from channel", "userInput", userInput)
						return
					}
					dispatchToolCalls := c.handleChoice(ctx, choiceResponse)
					if dispatchToolCalls {
						if err := c.DispatchToolCalls(ctx); err != nil {
							log.Error(err, "error dispatching tool calls")
							c.setAgentState(api.AgentStateDone)
							c.pendingFunctionCalls = []ToolCallAnalysis{}
							c.session.LastModified = time.Now()
							// In RunOnce mode, exit on tool execution error
							if c.RunOnce {
								c.setAgentState(api.AgentStateExited)
								return
							}
							continue
						}
						// Clear pending function calls after execution
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						c.setAgentState(api.AgentStateRunning)
						c.currIteration = c.currIteration + 1
					} else {
						// if user has declined, we are done with this iteration
						c.currIteration = c.currIteration + 1
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						c.setAgentState(api.AgentStateRunning)
						c.session.LastModified = time.Now()
					}
				}
			case api.AgentStateRunning:
				// Agent is running, don't wait for input, just continue to process the agentic loop
				log.Info("Agent is in running state, processing agentic loop")
			case api.AgentStateExited:
				log.Info("Agent exited in RunOnce mode")
				return
			}

			if c.AgentState() == api.AgentStateRunning {
				log.Info("Processing agentic loop", "currIteration", c.currIteration, "maxIterations", c.MaxIterations, "currChatContentLen", len(c.currChatContent))

				if c.currIteration >= c.MaxIterations {
					c.setAgentState(api.AgentStateDone)
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					c.addMessage(api.MessageSourceAgent, api.MessageTypeText, "Maximum number of iterations reached.")
					continue
				}

				// we run the agentic loop for one iteration
				stream, err := c.llmChat.SendStreaming(ctx, c.currChatContent...)
				if err != nil {
					log.Error(err, "error sending streaming LLM response")
					c.setAgentState(api.AgentStateDone)
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					continue
				}

				// Clear our "response" now that we sent the last response
				c.currChatContent = nil

				if c.EnableToolUseShim {
					// convert the candidate response into a gollm.ChatResponse
					stream, err = candidateToShimCandidate(stream)
					if err != nil {
						c.setAgentState(api.AgentStateDone)
						c.pendingFunctionCalls = []ToolCallAnalysis{}

						// In RunOnce mode, exit on shim conversion error
						if c.RunOnce {
							c.setAgentState(api.AgentStateExited)
							return
						}

						continue
					}
				}
				// Process each part of the response
				var functionCalls []gollm.FunctionCall

				// accumulator for streamed text
				var streamedText string
				var llmError error

				for response, err := range stream {
					log.Info("westnoah Trying to read response")
					if err != nil {
						log.Error(err, "error reading streaming LLM response")
						llmError = err
						c.setAgentState(api.AgentStateDone)
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						break
					}
					if response == nil {
						log.Info("westnoah Response was nil")
						// end of streaming response
						break
					}
					// klog.Infof("response: %+v", response)

					if len(response.Candidates()) == 0 {
						llmError = fmt.Errorf("no candidates in response")
						log.Error(nil, "No candidates in response")
						c.setAgentState(api.AgentStateDone)
						c.pendingFunctionCalls = []ToolCallAnalysis{}
						break
					}

					candidate := response.Candidates()[0]

					for _, part := range candidate.Parts() {
						log.Info("westnoah In the parts loop")
						// Check if it's a text response
						if text, ok := part.AsText(); ok {
							log.Info("text response", "text", text)
							streamedText += text
						}

						// Check if it's a function call
						if calls, ok := part.AsFunctionCalls(); ok && len(calls) > 0 {
							log.Info("function calls", "calls", calls)
							functionCalls = append(functionCalls, calls...)
						}
					}
				}
				if llmError != nil {
					log.Error(llmError, "error streaming LLM response")
					c.setAgentState(api.AgentStateDone)
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: "+llmError.Error())
					continue
				}
				log.Info("streamedText", "streamedText", streamedText)

				if streamedText != "" {
					c.addMessage(api.MessageSourceModel, api.MessageTypeText, streamedText)
				}
				// If no function calls to be made, we're done
				if len(functionCalls) == 0 {
					log.Info("No function calls to be made, so most likely the task is completed, so we're done.")
					c.setAgentState(api.AgentStateDone)
					c.currChatContent = []any{}
					c.currIteration = 0
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					log.Info("Agent task completed, transitioning to done state")
					continue
				}

				toolCallAnalysisResults, err := c.analyzeToolCalls(ctx, functionCalls)
				if err != nil {
					log.Error(err, "error analyzing tool calls")
					c.setAgentState(api.AgentStateDone)
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					c.session.LastModified = time.Now()
					c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: "+err.Error())
					continue
				}

				// mark the tools for dispatching
				c.pendingFunctionCalls = toolCallAnalysisResults

				interactiveToolCallIndex := -1
				modifiesResourceToolCallIndex := -1
				for i, result := range toolCallAnalysisResults {
					if result.ModifiesResourceStr != "no" {
						modifiesResourceToolCallIndex = i
					}
					if result.IsInteractive {
						interactiveToolCallIndex = i
					}
				}

				if interactiveToolCallIndex >= 0 {
					// Show error block for both shim enabled and disabled modes
					errorMessage := fmt.Sprintf("  %s\n", toolCallAnalysisResults[interactiveToolCallIndex].IsInteractiveError.Error())
					c.addMessage(api.MessageSourceAgent, api.MessageTypeError, errorMessage)

					if c.EnableToolUseShim {
						// Add the error as an observation
						observation := fmt.Sprintf("Result of running %q:\n%v",
							toolCallAnalysisResults[interactiveToolCallIndex].FunctionCall.Name,
							toolCallAnalysisResults[interactiveToolCallIndex].IsInteractiveError.Error())
						c.currChatContent = append(c.currChatContent, observation)
					} else {
						// For models with tool-use support (shim disabled), use proper FunctionCallResult
						// Note: This assumes the model supports sending FunctionCallResult
						c.currChatContent = append(c.currChatContent, gollm.FunctionCallResult{
							ID:     toolCallAnalysisResults[interactiveToolCallIndex].FunctionCall.ID,
							Name:   toolCallAnalysisResults[interactiveToolCallIndex].FunctionCall.Name,
							Result: map[string]any{"error": toolCallAnalysisResults[interactiveToolCallIndex].IsInteractiveError.Error()},
						})
					}
					c.pendingFunctionCalls = []ToolCallAnalysis{} // reset pending function calls
					c.currIteration = c.currIteration + 1
					continue // Skip execution for interactive commands
				}

				if !c.SkipPermissions && modifiesResourceToolCallIndex >= 0 {
					// In RunOnce mode, exit with error if permission is required
					if c.RunOnce {
						var commandDescriptions []string
						for _, call := range c.pendingFunctionCalls {
							commandDescriptions = append(commandDescriptions, call.ParsedToolCall.Description())
						}
						errorMessage := "RunOnce mode cannot handle permission requests. The following commands require approval:\n* " + strings.Join(commandDescriptions, "\n* ")
						errorMessage += "\nUse --skip-permissions flag to bypass permission checks in RunOnce mode."

						log.Error(nil, "RunOnce mode cannot handle permission requests", "commands", commandDescriptions)
						c.setAgentState(api.AgentStateExited)
						c.addMessage(api.MessageSourceAgent, api.MessageTypeError, errorMessage)
						return
					}

					var commandDescriptions []string
					for _, call := range c.pendingFunctionCalls {
						commandDescriptions = append(commandDescriptions, call.ParsedToolCall.Description())
					}
					confirmationPrompt := "The following commands require your approval to run:\n* " + strings.Join(commandDescriptions, "\n* ")
					confirmationPrompt += "\n\nDo you want to proceed ?"

					choiceRequest := &api.UserChoiceRequest{
						Prompt: confirmationPrompt,
						Options: []api.UserChoiceOption{
							{Value: "yes", Label: "Yes"},
							{Value: "yes_and_dont_ask_me_again", Label: "Yes, and don't ask me again"},
							{Value: "no", Label: "No"},
						},
					}
					c.setAgentState(api.AgentStateWaitingForInput)
					c.addMessage(api.MessageSourceAgent, api.MessageTypeUserChoiceRequest, choiceRequest)
					// Request input from the user by sending a message on the output channel.
					// Remaining part of the loop will be now resumed when we receive a choice input
					// from the user.
					continue
				}

				// we are here means we are in the clear to dispatch the tool calls
				if err := c.DispatchToolCalls(ctx); err != nil {
					log.Error(err, "error dispatching tool calls")
					c.setAgentState(api.AgentStateDone)
					c.pendingFunctionCalls = []ToolCallAnalysis{}
					c.session.LastModified = time.Now()
					c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Error: "+err.Error())
					continue
				}
				c.currIteration = c.currIteration + 1
				c.pendingFunctionCalls = []ToolCallAnalysis{}
				log.Info("Tool calls dispatched successfully", "currIteration", c.currIteration, "currChatContentLen", len(c.currChatContent), "agentState", c.AgentState())
			}
		}
	}()

	return nil
}

func (c *Agent) handleMetaQuery(ctx context.Context, query string) (answer string, handled bool, err error) {
	switch query {
	case "clear":
		c.sessionMu.Lock()
		c.session.Messages = []*api.Message{}
		c.sessionMu.Unlock()
		return "Cleared the conversation.", true, nil
	case "reset":
		// TODO(westnoah): dedup
		c.sessionMu.Lock()
		c.session.Messages = []*api.Message{}
		c.sessionMu.Unlock()
		if c.session.SessionDB != nil {
			if err := c.session.SessionDB.ClearChatMessages(); err != nil {
				return "", false, fmt.Errorf("clearing data store: %w", err)
			}
		}
		err := c.Init(ctx)
		if err != nil {
			return "", false, err
		}
		return "Reset the conversation.", true, nil
	case "compact":
		return "Compaction not yet implemented.", true, nil
	case "model":
		return "Current model is `" + c.Model + "`", true, nil
	case "models":
		models, err := c.listModels(ctx)
		if err != nil {
			return "", false, fmt.Errorf("listing models: %w", err)
		}
		return "Available models:\n\n  - " + strings.Join(models, "\n  - ") + "\n\n", true, nil
	case "tools":
		return "Available tools:\n\n  - " + strings.Join(c.Tools.Names(), "\n  - ") + "\n\n", true, nil
	case "session":
		if c.session.SessionDB == nil {
			return "No active session.", true, nil
		}
		metadata, err := c.session.SessionDB.LoadMetadata()
		if err != nil {
			return "", false, fmt.Errorf("failed to load session metadata: %w", err)
		}
		sessionString := fmt.Sprintf("SessionID: %s\n", c.session.SessionDB.ID) + metadata.String()
		return sessionString, true, nil
	case "sessions":
		manager, err := sessions.NewSessionManager()
		if err != nil {
			return "", false, fmt.Errorf("failed to create session manager: %w", err)
		}
		sessionList, err := manager.ListSessions()
		if err != nil {
			return "", false, fmt.Errorf("failed to list sessions: %w", err)
		}
		if len(sessionList) == 0 {
			return "No sessions found.", true, nil
		}
		sessionListString := "Available sessions:\n"
		for _, session := range sessionList {
			metadata, err := session.LoadMetadata()
			if err != nil {
				sessionListString += fmt.Sprintf("%s: <error loading metadata\n", session.ID)
				continue
			}
			sessionString := fmt.Sprintf("\nSessionID: %s\n", session.ID) + metadata.String()
			sessionListString += sessionString
		}
		return sessionListString, true, nil
	}


	return "", false, nil
}

func (c *Agent) listModels(ctx context.Context) ([]string, error) {
	if c.availableModels == nil {
		modelNames, err := c.LLM.ListModels(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing models: %w", err)
		}
		c.availableModels = modelNames
	}
	return c.availableModels, nil
}

func (c *Agent) DispatchToolCalls(ctx context.Context) error {
	log := klog.FromContext(ctx)
	// execute all pending function calls
	for _, call := range c.pendingFunctionCalls {
		// Only show "Running" message and proceed with execution for non-interactive commands
		toolDescription := call.ParsedToolCall.Description()

		c.addMessage(api.MessageSourceModel, api.MessageTypeToolCallRequest, toolDescription)

		output, err := call.ParsedToolCall.InvokeTool(ctx, tools.InvokeToolOptions{
			Kubeconfig: c.Kubeconfig,
			WorkDir:    c.workDir,
		})
		if err != nil {
			log.Error(err, "error executing action", "output", output)
			return err
		}

		// Handle timeout message using UI blocks
		if execResult, ok := output.(*tools.ExecResult); ok && execResult != nil && execResult.StreamType == "timeout" {
			c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "\nTimeout reached after 7 seconds\n")
		}
		// Add the tool call result to maintain conversation flow
		var payload any
		if c.EnableToolUseShim {
			// Add the error as an observation
			observation := fmt.Sprintf("Result of running %q:\n%v",
				call.FunctionCall.Name,
				output)
			c.currChatContent = append(c.currChatContent, observation)
			payload = observation
		} else {
			// If shim is disabled, convert the result to a map and append FunctionCallResult
			result, err := tools.ToolResultToMap(output)
			if err != nil {
				log.Error(err, "error converting tool result to map", "output", output)
				return err
			}
			payload = result
			c.currChatContent = append(c.currChatContent, gollm.FunctionCallResult{
				ID:     call.FunctionCall.ID,
				Name:   call.FunctionCall.Name,
				Result: result,
			})
		}
		c.addMessage(api.MessageSourceAgent, api.MessageTypeToolCallResponse, payload)
	}
	return nil
}

// The key idea is to treat all tool calls to be executed atomically or not
// If all tool calls are readonly call, it is straight forward
// if some of the tool calls are not readonly, then the interesting question is should the permission
// be asked for each of the tool call or only once for all the tool calls.
// I think treating all tool calls as atomic is the right thing to do.

type ToolCallAnalysis struct {
	FunctionCall        gollm.FunctionCall
	ParsedToolCall      *tools.ToolCall
	IsInteractive       bool
	IsInteractiveError  error
	ModifiesResourceStr string
}

func (c *Agent) analyzeToolCalls(ctx context.Context, toolCalls []gollm.FunctionCall) ([]ToolCallAnalysis, error) {
	toolCallAnalysis := make([]ToolCallAnalysis, len(toolCalls))
	for i, call := range toolCalls {
		toolCallAnalysis[i].FunctionCall = call
		toolCall, err := c.Tools.ParseToolInvocation(ctx, call.Name, call.Arguments)
		if err != nil {
			return nil, fmt.Errorf("error parsing tool call: %w", err)
		}
		toolCallAnalysis[i].IsInteractive, err = toolCall.GetTool().IsInteractive(call.Arguments)
		if err != nil {
			toolCallAnalysis[i].IsInteractiveError = err
		}
		modifiesResourceStr := toolCall.GetTool().CheckModifiesResource(call.Arguments)
		if modifiesResourceStr == "unknown" {
			if llmModifies, ok := call.Arguments["modifies_resource"].(string); ok {
				modifiesResourceStr = llmModifies
			}
		}
		toolCallAnalysis[i].ModifiesResourceStr = modifiesResourceStr
		toolCallAnalysis[i].ParsedToolCall = toolCall
	}
	return toolCallAnalysis, nil
}

func (c *Agent) handleChoice(ctx context.Context, choice *api.UserChoiceResponse) (dispatchToolCalls bool) {
	log := klog.FromContext(ctx)
	// if user input is a choice and use has declined the operation,
	// we need to abort all pending function calls.
	// update the currChatContent with the choice and keep the agent loop running.

	// Normalize the input
	switch choice.Choice {
	case 1:
		dispatchToolCalls = true
	case 2:
		c.SkipPermissions = true
		dispatchToolCalls = true
	case 3:
		c.currChatContent = append(c.currChatContent, gollm.FunctionCallResult{
			ID:   c.pendingFunctionCalls[0].FunctionCall.ID,
			Name: c.pendingFunctionCalls[0].FunctionCall.Name,
			Result: map[string]any{
				"error":     "User declined to run this operation.",
				"status":    "declined",
				"retryable": false,
			},
		})
		c.pendingFunctionCalls = []ToolCallAnalysis{}
		dispatchToolCalls = false
		c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Operation was skipped. User declined to run this operation.")
	default:
		// This case should technically not be reachable due to AskForConfirmation loop
		err := fmt.Errorf("invalid confirmation choice: %q", choice.Choice)
		log.Error(err, "Invalid choice received from AskForConfirmation")
		c.pendingFunctionCalls = []ToolCallAnalysis{}
		dispatchToolCalls = false
		c.addMessage(api.MessageSourceAgent, api.MessageTypeError, "Invalid choice received. Cancelling operation.")
	}
	return dispatchToolCalls
}

// generateFromTemplate generates a prompt for LLM. It uses the prompt from the provides template file or default.
func (a *Agent) generatePrompt(_ context.Context, defaultPromptTemplate string, data PromptData) (string, error) {
	promptTemplate := defaultPromptTemplate
	if a.PromptTemplateFile != "" {
		content, err := os.ReadFile(a.PromptTemplateFile)
		if err != nil {
			return "", fmt.Errorf("error reading template file: %v", err)
		}
		promptTemplate = string(content)
	}

	for _, extraPromptPath := range a.ExtraPromptPaths {
		content, err := os.ReadFile(extraPromptPath)
		if err != nil {
			return "", fmt.Errorf("error reading extra prompt path: %v", err)
		}
		promptTemplate += "\n" + string(content)
	}

	tmpl, err := template.New("promptTemplate").Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("building template for prompt: %w", err)
	}

	var result strings.Builder
	err = tmpl.Execute(&result, &data)
	if err != nil {
		return "", fmt.Errorf("evaluating template for prompt: %w", err)
	}
	return result.String(), nil
}

// PromptData represents the structure of the data to be filled into the template.
type PromptData struct {
	Query string
	Tools tools.Tools

	EnableToolUseShim bool
}

func (a *PromptData) ToolsAsJSON() string {
	var toolDefinitions []*gollm.FunctionDefinition

	for _, tool := range a.Tools.AllTools() {
		toolDefinitions = append(toolDefinitions, tool.FunctionDefinition())
	}

	json, err := json.MarshalIndent(toolDefinitions, "", "  ")
	if err != nil {
		return ""
	}
	return string(json)
}

func (a *PromptData) ToolNames() string {
	return strings.Join(a.Tools.Names(), ", ")
}

type ReActResponse struct {
	Thought string  `json:"thought"`
	Answer  string  `json:"answer,omitempty"`
	Action  *Action `json:"action,omitempty"`
}

type Action struct {
	Name             string `json:"name"`
	Reason           string `json:"reason"`
	Command          string `json:"command"`
	ModifiesResource string `json:"modifies_resource"`
}

func extractJSON(s string) (string, bool) {
	const jsonBlockMarker = "```json"

	first := strings.Index(s, jsonBlockMarker)
	last := strings.LastIndex(s, "```")
	if first == -1 || last == -1 || first == last {
		return "", false
	}
	data := s[first+len(jsonBlockMarker) : last]

	return data, true
}

// parseReActResponse parses the LLM response into a ReActResponse struct
// This function assumes the input contains exactly one JSON code block
// formatted with ```json and ``` markers. The JSON block is expected to
// contain a valid ReActResponse object.
func parseReActResponse(input string) (*ReActResponse, error) {
	cleaned, found := extractJSON(input)
	if !found {
		return nil, fmt.Errorf("no JSON code block found in %q", cleaned)
	}

	cleaned = strings.ReplaceAll(cleaned, "\n", "")
	cleaned = strings.TrimSpace(cleaned)

	var reActResp ReActResponse
	if err := json.Unmarshal([]byte(cleaned), &reActResp); err != nil {
		return nil, fmt.Errorf("parsing JSON %q: %w", cleaned, err)
	}
	return &reActResp, nil
}

// toMap converts the value to a map, going via JSON
func toMap(v any) (map[string]any, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("converting %T to json: %w", v, err)
	}
	m := make(map[string]any)
	if err := json.Unmarshal(j, &m); err != nil {
		return nil, fmt.Errorf("converting json to map: %w", err)
	}
	return m, nil
}

func candidateToShimCandidate(iterator gollm.ChatResponseIterator) (gollm.ChatResponseIterator, error) {
	return func(yield func(gollm.ChatResponse, error) bool) {
		buffer := ""
		for response, err := range iterator {
			if err != nil {
				yield(nil, err)
				return
			}

			if len(response.Candidates()) == 0 {
				yield(nil, fmt.Errorf("no candidates in LLM response"))
				return
			}

			candidate := response.Candidates()[0]

			for _, part := range candidate.Parts() {
				if text, ok := part.AsText(); ok {
					buffer += text
					klog.Infof("text is %q", text)
				} else {
					yield(nil, fmt.Errorf("no text part found in candidate"))
					return
				}
			}
		}

		if buffer == "" {
			yield(nil, nil)
			return
		}

		parsedReActResp, err := parseReActResponse(buffer)
		if err != nil {
			yield(nil, fmt.Errorf("parsing ReAct response %q: %w", buffer, err))
			return
		}
		buffer = "" // TODO: any trailing text?
		yield(&ShimResponse{candidate: parsedReActResp}, nil)
	}, nil
}

type ShimResponse struct {
	candidate *ReActResponse
}

func (r *ShimResponse) UsageMetadata() any {
	return nil
}

func (r *ShimResponse) Candidates() []gollm.Candidate {
	return []gollm.Candidate{&ShimCandidate{candidate: r.candidate}}
}

type ShimCandidate struct {
	candidate *ReActResponse
}

func (c *ShimCandidate) String() string {
	return fmt.Sprintf("Thought: %s\nAnswer: %s\nAction: %s", c.candidate.Thought, c.candidate.Answer, c.candidate.Action)
}

func (c *ShimCandidate) Parts() []gollm.Part {
	var parts []gollm.Part
	if c.candidate.Thought != "" {
		parts = append(parts, &ShimPart{text: c.candidate.Thought})
	}
	if c.candidate.Answer != "" {
		parts = append(parts, &ShimPart{text: c.candidate.Answer})
	}
	if c.candidate.Action != nil {
		parts = append(parts, &ShimPart{action: c.candidate.Action})
	}
	return parts
}

type ShimPart struct {
	text   string
	action *Action
}

func (p *ShimPart) AsText() (string, bool) {
	return p.text, p.text != ""
}

func (p *ShimPart) AsFunctionCalls() ([]gollm.FunctionCall, bool) {
	if p.action != nil {
		functionCallArgs, err := toMap(p.action)
		if err != nil {
			return nil, false
		}
		delete(functionCallArgs, "name") // passed separately
		// delete(functionCallArgs, "reason")
		// delete(functionCallArgs, "modifies_resource")
		return []gollm.FunctionCall{
			{
				Name:      p.action.Name,
				Arguments: functionCallArgs,
			},
		}, true
	}
	return nil, false
}
