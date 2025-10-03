// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gollm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/GoogleCloudPlatform/kubectl-ai/pkg/journal"

	"k8s.io/klog/v2"
)

// journalingRoundTripper wraps an existing http.RoundTripper to record requests and responses.
type journalingRoundTripper struct {
	next http.RoundTripper // The actual transport that does the network call
}

// RoundTrip satisfies the http.RoundTripper interface. It intercepts an HTTP request,
// logs it, passes it to the next handler, and then logs the response.
// It includes special handling to correctly parse and summarize streaming responses.
func (jrt *journalingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := journal.RecorderFromContext(req.Context())

	// Log the outgoing request.
	reqBytes, err := httputil.DumpRequestOut(req, true)
	if err == nil {
		err = recorder.Write(req.Context(), &journal.Event{
			Action:  journal.ActionHTTPRequest,
			Payload: map[string]any{"request": string(reqBytes)},
		})
		if err != nil {
			klog.Errorf("Error writing outgoing request to journal: %v", err)
		}
	}

	// Pass the request to the next RoundTripper to make the actual network call.
	resp, err := jrt.next.RoundTrip(req)
	if err != nil {
		writeErr := recorder.Write(req.Context(), &journal.Event{
			Action:  journal.ActionHTTPError,
			Payload: map[string]any{"error": "http transport failed", "detail": err.Error()},
		})
		if writeErr != nil {
			klog.Errorf("Error writing RoundTripper error to journal: %v", writeErr)
		}
		klog.Errorf("RoundTripper error: %v", err)
		return nil, err
	}

	// Read the entire response body so we can log it and then pass it along.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		// handle error
		klog.Errorf("Error reading response body (for logging): %v", err)
		return nil, err
	}
	resp.Body.Close() // Close the original body

	// Default payload is the raw body, for non-streaming responses.
	logPayload := map[string]any{
		"status":  resp.Status,
		"headers": resp.Header,
		"body":    string(bodyBytes),
	}

	// If the response is a stream, process it for a cleaner log.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		summarizedPayload := processStream(bodyBytes)
		logPayload["body"] = summarizedPayload // Replace raw body with the summary
	}

	// Write the final event to the journal.
	err = recorder.Write(req.Context(), &journal.Event{
		Action:  journal.ActionHTTPResponse,
		Payload: logPayload,
	})
	if err != nil {
		// Log the error and continue
		klog.Errorf("Error writing to journal: %v", err)
	}

	// IMPORTANT: Return the original, untouched body to the client.
	resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	return resp, nil
}

// processStream parses a Server-Sent Events (SSE) stream and returns a structured summary.
// It aggregates fragmented text and reassembles complete tool calls.
func processStream(body []byte) map[string]any {
	var aggregatedText strings.Builder
	// A map to hold the in-progress tool calls, keyed by their index.
	toolCallBuilders := make(map[int]map[string]any)

	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData == "[DONE]" {
			break
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue // Skip malformed JSON chunks
		}

		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}

		// Aggregate text content
		if content, ok := delta["content"].(string); ok && content != "" {
			aggregatedText.WriteString(content)
		}

		// Aggregate tool calls
		if toolCallChunks, ok := delta["tool_calls"].([]any); ok {
			for _, toolCallChunk := range toolCallChunks {
				tc, ok := toolCallChunk.(map[string]any)
				if !ok {
					continue
				}

				indexFloat, ok := tc["index"].(float64)
				if !ok {
					continue
				}
				index := int(indexFloat)

				if _, exists := toolCallBuilders[index]; !exists {
					toolCallBuilders[index] = make(map[string]any)
					toolCallBuilders[index]["function"] = tc["function"]
					toolCallBuilders[index]["arguments_builder"] = &strings.Builder{}
				}

				if functionDelta, ok := tc["function"].(map[string]any); ok {
					if args, ok := functionDelta["arguments"].(string); ok {
						if builder, ok := toolCallBuilders[index]["arguments_builder"].(*strings.Builder); ok {
							builder.WriteString(args)
						}
					}
				}
			}
		}
	}

	// Finalize the tool calls from the builders.
	var significantEvents []map[string]any
	for _, builder := range toolCallBuilders {
		if function, ok := builder["function"].(map[string]any); ok {
			if argBuilder, ok := builder["arguments_builder"].(*strings.Builder); ok {
				function["arguments"] = argBuilder.String()
			}
		}
		significantEvents = append(significantEvents, map[string]any{"tool_calls": []any{builder}})
	}

	return map[string]any{
		"aggregated_text":    aggregatedText.String(),
		"significant_events": significantEvents,
	}
}

// withJournaling is a decorator function that wraps an http.Client's transport
// with the journalingRoundTripper, but only if a recorder is found in the context.
func withJournaling(client *http.Client) *http.Client {
	// wrap the transport
	client.Transport = &journalingRoundTripper{
		next: client.Transport,
	}

	return client
}
