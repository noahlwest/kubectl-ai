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

package api

import (
	"time"

	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/sessions"
)

type Session struct {
	ID           string
	Messages     []*Message
	AgentState   AgentState
	CreatedAt    time.Time
	LastModified time.Time
	// MCP status information
	MCPStatus *MCPStatus
	// sessionDB is the underlying data persistence layer for the session.
	SessionDB *sessions.Session
}

type AgentState string

const (
	AgentStateIdle            AgentState = "idle"
	AgentStateWaitingForInput AgentState = "waiting-for-input"
	AgentStateRunning         AgentState = "running"
	AgentStateInitializing    AgentState = "initializing"
	AgentStateDone            AgentState = "done"
	AgentStateExited          AgentState = "exited"
)

type MessageType string

const (
	MessageTypeText               MessageType = "text"
	MessageTypeError              MessageType = "error"
	MessageTypeToolCallRequest    MessageType = "tool-call-request"
	MessageTypeToolCallResponse   MessageType = "tool-call-response"
	MessageTypeUserInputRequest   MessageType = "user-input-request"
	MessageTypeUserInputResponse  MessageType = "user-input-response"
	MessageTypeUserChoiceRequest  MessageType = "user-choice-request"
	MessageTypeUserChoiceResponse MessageType = "user-choice-response"
)

type Message struct {
	ID        string
	Source    MessageSource
	Type      MessageType
	Payload   any
	Timestamp time.Time
}

type MessageSource string

const (
	MessageSourceUser  MessageSource = "user"
	MessageSourceAgent MessageSource = "agent"
	MessageSourceModel MessageSource = "model"
)

type UserChoiceRequest struct {
	Prompt  string
	Options []UserChoiceOption
}

type UserChoiceOption struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

type UserChoiceResponse struct {
	Choice int `json:"choice"`
}

type UserInputResponse struct {
	Query string `json:"query"`
}

// MCPStatus represents the overall status of MCP servers and tools
type MCPStatus struct {
	ServerInfoList []ServerConnectionInfo `json:"serverInfoList,omitempty"`
	TotalServers   int                    `json:"totalServers,omitempty"`
	ConnectedCount int                    `json:"connectedCount,omitempty"`
	FailedCount    int                    `json:"failedCount,omitempty"`
	TotalTools     int                    `json:"totalTools,omitempty"`
	ClientEnabled  bool                   `json:"clientEnabled,omitempty"`
}

// ServerConnectionInfo holds connection status for a single MCP server
type ServerConnectionInfo struct {
	Name           string    `json:"name,omitempty"`
	Command        string    `json:"command,omitempty"`
	IsLegacy       bool      `json:"isLegacy,omitempty"`
	IsConnected    bool      `json:"isConnected,omitempty"`
	AvailableTools []MCPTool `json:"availableTools,omitempty"`
}

// MCPTool represents an MCP tool with basic information
type MCPTool struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Server      string `json:"server,omitempty"`
}

func (s *Session) AllMessages() []*Message {
	return s.Messages
}
